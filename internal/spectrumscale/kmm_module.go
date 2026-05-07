package spectrumscale

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func PrintKMMModulesInFusionAccess(mc *kube.Context) error {
	list, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping %s listing (resume after namespace removed)", constants.FusionAccessNS, constants.KmmModulesResource))
			return nil
		}
		return fmt.Errorf("list %s in %s: %w", constants.KmmModulesResource, constants.FusionAccessNS, err)
	}

	output.PrintInfo(fmt.Sprintf("%s in namespace %s (oc get %s -n %s):",
		constants.KmmModulesResource, constants.FusionAccessNS, constants.KmmModulesResource, constants.FusionAccessNS))
	if len(list.Items) == 0 {
		output.PrintInfo("  (none)")
		return nil
	}
	for _, m := range list.Items {
		fin := m.GetFinalizers()
		finDesc := "(none)"
		if len(fin) > 0 {
			finDesc = strings.Join(fin, ", ")
		}
		output.PrintInfo(fmt.Sprintf(
			"  name=%s  generation=%d  resourceVersion=%s  finalizers=[%s]",
			m.GetName(), m.GetGeneration(), m.GetResourceVersion(), finDesc,
		))
	}
	return nil
}

// resolveScaleImageDigestFromNodes returns scale.spectrum.ibm.com/image-digest from a storage node when possible,
// otherwise from the first node that carries the label.
func resolveScaleImageDigestFromNodes(mc *kube.Context) (string, error) {
	nodes, err := mc.Clientset.CoreV1().Nodes().List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	var fallback string
	for i := range nodes.Items {
		labels := nodes.Items[i].Labels
		if labels == nil {
			continue
		}
		d, ok := labels[constants.ScaleNodeLabelImageDigest]
		if !ok || d == "" {
			continue
		}
		if labels[constants.ScaleNodeLabelRole] == constants.ScaleNodeRoleStorage {
			return d, nil
		}
		if fallback == "" {
			fallback = d
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no node has non-empty label %q", constants.ScaleNodeLabelImageDigest)
}

// kmmModuleNodeSelectorPaths are tried in order. Some clusters use spec.moduleLoader.selector; others
// (e.g. certain Module shapes) use spec.selector at the same level as moduleLoader.
var kmmModuleNodeSelectorPaths = [][]string{
	{"spec", "moduleLoader", "selector"},
	{"spec", "selector"},
}

func readKMMModuleNodeSelector(obj map[string]interface{}) (path []string, sel map[string]string, err error) {
	for _, p := range kmmModuleNodeSelectorPaths {
		sel, found, nerr := unstructured.NestedStringMap(obj, p...)
		if nerr != nil {
			return nil, nil, fmt.Errorf("invalid selector at %s: %w", strings.Join(p, "."), nerr)
		}
		if found {
			return p, sel, nil
		}
	}
	return nil, nil, fmt.Errorf("neither spec.moduleLoader.selector nor spec.selector present")
}

// PatchFusionAccessKMMModuleSelectorForMigration updates the single KMM Module in ibm-fusion-access: set
// scale.spectrum.ibm.com/image-digest from node labels only if that key is not already in the selector, and
// remove kubernetes.io/arch and scale.spectrum.ibm.com/role from the Module node selector (spec.moduleLoader.selector or spec.selector).
func PatchFusionAccessKMMModuleSelectorForMigration(mc *kube.Context) error {
	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS)
	list, err := res.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping KMM Module selector patch (resume)", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("list %s in %s: %w", constants.KmmModulesResource, constants.FusionAccessNS, err)
	}
	switch n := len(list.Items); n {
	case 0:
		return fmt.Errorf("expected exactly one %s in %s, found 0", constants.KmmModulesResource, constants.FusionAccessNS)
	case 1:
	default:
		return fmt.Errorf("expected exactly one %s in %s, found %d", constants.KmmModulesResource, constants.FusionAccessNS, n)
	}

	moduleName := list.Items[0].GetName()
	if mc.DryRun {
		_, sel, err := readKMMModuleNodeSelector(list.Items[0].Object)
		if err != nil {
			return fmt.Errorf("%s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		if _, has := sel[constants.ScaleNodeLabelImageDigest]; has {
			output.PrintDryRun(fmt.Sprintf(
				"Would patch %s %q in %s: remove selector keys %q and %q if present (%q already set — would not change)",
				constants.KmmModulesResource, moduleName, constants.FusionAccessNS,
				constants.NodeLabelArch, constants.ScaleNodeLabelRole,
				constants.ScaleNodeLabelImageDigest,
			))
			return nil
		}
		digest, err := resolveScaleImageDigestFromNodes(mc)
		if err != nil {
			return err
		}
		output.PrintDryRun(fmt.Sprintf(
			"Would patch %s %q in %s: set selector %q=%q; remove selector keys %q and %q if present",
			constants.KmmModulesResource, moduleName, constants.FusionAccessNS,
			constants.ScaleNodeLabelImageDigest, digest,
			constants.NodeLabelArch, constants.ScaleNodeLabelRole,
		))
		return nil
	}

	const maxAttempts = 15
	var cachedDigest string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		obj, err := res.Get(mc.Ctx, moduleName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get %s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		selPath, sel, err := readKMMModuleNodeSelector(obj.Object)
		if err != nil {
			return fmt.Errorf("%s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		out := make(map[string]string, len(sel)+1)
		for k, v := range sel {
			out[k] = v
		}
		delete(out, constants.NodeLabelArch)
		delete(out, constants.ScaleNodeLabelRole)
		digestAdded := false
		if _, has := sel[constants.ScaleNodeLabelImageDigest]; !has {
			if cachedDigest == "" {
				cachedDigest, err = resolveScaleImageDigestFromNodes(mc)
				if err != nil {
					return err
				}
			}
			out[constants.ScaleNodeLabelImageDigest] = cachedDigest
			digestAdded = true
		}
		if maps.Equal(sel, out) {
			output.PrintSkip(fmt.Sprintf(
				"%s %q in %s: selector already satisfies migration (nothing to change)",
				constants.KmmModulesResource, moduleName, constants.FusionAccessNS,
			))
			return nil
		}
		if err := unstructured.SetNestedStringMap(obj.Object, out, selPath...); err != nil {
			return fmt.Errorf("set selector on %s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		_, err = res.Update(mc.Ctx, obj, metav1.UpdateOptions{})
		if err == nil {
			if digestAdded {
				output.PrintSuccess(fmt.Sprintf(
					"Patched %s %q in %s (set %q; removed %q and %q if present)",
					constants.KmmModulesResource, moduleName, constants.FusionAccessNS,
					constants.ScaleNodeLabelImageDigest, constants.NodeLabelArch, constants.ScaleNodeLabelRole,
				))
			} else {
				output.PrintSuccess(fmt.Sprintf(
					"Patched %s %q in %s (%q already set — not updated; removed %q and %q if present)",
					constants.KmmModulesResource, moduleName, constants.FusionAccessNS,
					constants.ScaleNodeLabelImageDigest, constants.NodeLabelArch, constants.ScaleNodeLabelRole,
				))
			}
			return nil
		}
		if apierrors.IsConflict(err) && attempt < maxAttempts-1 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		return fmt.Errorf("update %s %q: %w", constants.KmmModulesResource, moduleName, err)
	}
	return fmt.Errorf("update %s %q: exhausted retries", constants.KmmModulesResource, moduleName)
}

func readNodesMatchingSelectorNumber(obj map[string]interface{}) (n int64, reported bool, err error) {
	v, found, err := unstructured.NestedFieldNoCopy(obj, "status", "moduleLoader", "nodesMatchingSelectorNumber")
	if err != nil {
		return 0, false, err
	}
	if found && v != nil {
		switch x := v.(type) {
		case int64:
			return x, true, nil
		case int32:
			return int64(x), true, nil
		case int:
			return int64(x), true, nil
		case float64:
			return int64(x), true, nil
		default:
			return 0, true, fmt.Errorf("unexpected type %T for nodesMatchingSelectorNumber", v)
		}
	}
	ml, mlFound, err := unstructured.NestedMap(obj, "status", "moduleLoader")
	if err != nil {
		return 0, false, err
	}
	if mlFound && len(ml) == 0 {
		return 0, true, nil
	}
	return 0, false, nil
}

func waitForKMMModuleLoaderNodesMatchingSelectorZero(mc *kube.Context, namespace, moduleName string, timeout, poll time.Duration) error {
	var loggedWaiting bool
	iter := 0
	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		iter++
		mod, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(namespace).Get(mc.Ctx, moduleName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("%s %q not found in %s — treating nodesMatchingSelectorNumber wait as satisfied", constants.KmmModulesResource, moduleName, namespace))
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("get %s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		val, reported, rerr := readNodesMatchingSelectorNumber(mod.Object)
		if rerr != nil {
			return false, fmt.Errorf("%s %q: %w", constants.KmmModulesResource, moduleName, rerr)
		}
		if !reported {
			if !loggedWaiting {
				output.PrintInfo(fmt.Sprintf("Waiting for %s %q status.moduleLoader.nodesMatchingSelectorNumber to be reported...", constants.KmmModulesResource, moduleName))
				loggedWaiting = true
			}
			return false, nil
		}
		if val == 0 {
			output.PrintSuccess(fmt.Sprintf("%s %q: status.moduleLoader.nodesMatchingSelectorNumber is 0", constants.KmmModulesResource, moduleName))
			return true, nil
		}

		if iter == 1 || iter%12 == 0 {
			output.PrintInfo(fmt.Sprintf(
				"Waiting for %s %q status.moduleLoader.nodesMatchingSelectorNumber==0 (current %d)...",
				constants.KmmModulesResource, moduleName, val,
			))
		}
		return false, nil
	}, timeout, poll, fmt.Sprintf("status.moduleLoader.nodesMatchingSelectorNumber==0 on %s %s/%s", constants.KmmModulesResource, namespace, moduleName))
	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		return fmt.Errorf(
			"timeout after %v waiting for status.moduleLoader.nodesMatchingSelectorNumber==0 on %s %s/%s",
			timeout, constants.KmmModulesResource, namespace, moduleName,
		)
	}
	return err
}

// WaitForFusionAccessKMMModuleLoaderNodesMatchingZero polls the single Module in ibm-fusion-access until
// status.moduleLoader.nodesMatchingSelectorNumber is 0. Skips if the namespace or module is already gone.
func WaitForFusionAccessKMMModuleLoaderNodesMatchingZero(mc *kube.Context) error {
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would wait up to %v for status.moduleLoader.nodesMatchingSelectorNumber==0 on the KMM Module in %s (poll every %v)",
			constants.KmmModuleNodesMatchingWaitTimeout, constants.FusionAccessNS,
			constants.KmmModuleNodesMatchingPollInterval,
		))
		return nil
	}
	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS)
	list, err := res.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping wait for nodesMatchingSelectorNumber", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("list %s in %s: %w", constants.KmmModulesResource, constants.FusionAccessNS, err)
	}
	if len(list.Items) == 0 {
		output.PrintSkip(fmt.Sprintf("No %s in %s — skipping wait for nodesMatchingSelectorNumber", constants.KmmModulesResource, constants.FusionAccessNS))
		return nil
	}
	if len(list.Items) != 1 {
		return fmt.Errorf("expected exactly one %s in %s, found %d", constants.KmmModulesResource, constants.FusionAccessNS, len(list.Items))
	}
	name := list.Items[0].GetName()
	output.PrintInfo(fmt.Sprintf(
		"Waiting up to %v for %s %q in %s: status.moduleLoader.nodesMatchingSelectorNumber == 0 (poll every %v)...",
		constants.KmmModuleNodesMatchingWaitTimeout, constants.KmmModulesResource, name, constants.FusionAccessNS,
		constants.KmmModuleNodesMatchingPollInterval,
	))
	return waitForKMMModuleLoaderNodesMatchingSelectorZero(
		mc, constants.FusionAccessNS, name,
		constants.KmmModuleNodesMatchingWaitTimeout,
		constants.KmmModuleNodesMatchingPollInterval,
	)
}

// DeleteFusionAccessSingletonKMMModuleStripFinalizers deletes the single KMM Module in ibm-fusion-access and waits until
// it is removed, clearing metadata.finalizers on each poll when present so deletion is not stuck.
// Skips if the namespace is gone or the module is already absent.
func DeleteFusionAccessSingletonKMMModuleStripFinalizers(mc *kube.Context) error {
	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS)
	list, err := res.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping KMM Module delete", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("list %s in %s: %w", constants.KmmModulesResource, constants.FusionAccessNS, err)
	}
	if len(list.Items) == 0 {
		output.PrintSkip(fmt.Sprintf("No %s in %s — skip delete (already removed)", constants.KmmModulesResource, constants.FusionAccessNS))
		return nil
	}
	if len(list.Items) != 1 {
		return fmt.Errorf("expected exactly one %s in %s, found %d", constants.KmmModulesResource, constants.FusionAccessNS, len(list.Items))
	}
	name := list.Items[0].GetName()
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would delete %s %q from %s and wait up to %v for removal (clearing finalizers if the object is stuck)",
			constants.KmmModulesResource, name, constants.FusionAccessNS, constants.KmmModuleDeleteWaitTimeout,
		))
		return nil
	}
	delErr := res.Delete(mc.Ctx, name, metav1.DeleteOptions{})
	if delErr != nil && !apierrors.IsNotFound(delErr) {
		return fmt.Errorf("delete %s %q: %w", constants.KmmModulesResource, name, delErr)
	}
	if apierrors.IsNotFound(delErr) {
		output.PrintSkip(fmt.Sprintf("%s %q already deleted from %s", constants.KmmModulesResource, name, constants.FusionAccessNS))
	} else {
		output.PrintSuccess(fmt.Sprintf("Deleted %s %q from %s", constants.KmmModulesResource, name, constants.FusionAccessNS))
	}
	output.PrintInfo(fmt.Sprintf(
		"Waiting up to %v for %s %q in %s to be fully removed...",
		constants.KmmModuleDeleteWaitTimeout, constants.KmmModulesResource, name, constants.FusionAccessNS,
	))
	if err := waitForKMMModuleGone(
		mc, constants.FusionAccessNS, name,
		constants.KmmModuleDeleteWaitTimeout,
		constants.KmmModuleDeletePollInterval,
	); err != nil {
		return fmt.Errorf("KMM module %q: %w", name, err)
	}
	output.PrintSuccess(fmt.Sprintf("%s %q is gone from %s", constants.KmmModulesResource, name, constants.FusionAccessNS))
	return nil
}

func clearKMMModuleFinalizers(mc *kube.Context, namespace, moduleName string) error {
	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(namespace)
	return helpers.ClearFinalizers(mc.Ctx, res, moduleName, constants.KmmModulesResource, 12)
}

func waitForKMMModuleGone(mc *kube.Context, namespace, moduleName string, timeout, poll time.Duration) error {
	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(namespace)
	return waitForDynamicResourceGone(
		mc, res, moduleName, constants.KmmModulesResource, timeout, poll,
		fmt.Sprintf("%s %s/%s removed", constants.KmmModulesResource, namespace, moduleName),
		"(KMM may re-add them)",
		func() error { return clearKMMModuleFinalizers(mc, namespace, moduleName) },
		func(err error) {
			output.PrintWarning(fmt.Sprintf("could not clear finalizers on %q (will retry): %v", moduleName, err))
		},
		func(t time.Duration, fin []string) error {
			return fmt.Errorf(
				"timeout after %v: %s %s/%s still exists (finalizers=%v); if finalizers persist, the KMM controller may be reconciling this Module",
				t, constants.KmmModulesResource, namespace, moduleName, fin,
			)
		},
	)
}
