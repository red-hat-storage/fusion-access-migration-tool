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

const (
	moduleLoaderFieldNodesMatchingSelectorNumber = "nodesMatchingSelectorNumber"
	moduleLoaderFieldDesiredNumber               = "desiredNumber"
	moduleLoaderFieldAvailableNumber             = "availableNumber"
)

func formatKMMModule(namespace, moduleName string) string {
	return fmt.Sprintf("%s %q in %s", constants.KmmModulesResource, moduleName, namespace)
}

func kmmModuleNamespaceMissing(mc *kube.Context, namespace string) (bool, error) {
	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	return false, fmt.Errorf("checking namespace %s: %w", namespace, err)
}

func getKMMModuleName(mc *kube.Context, namespace string) (string, bool, error) {
	list, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(namespace).List(
		mc.Ctx, metav1.ListOptions{Limit: 2},
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("list %s in %s: %w", constants.KmmModulesResource, namespace, err)
	}
	switch len(list.Items) {
	case 0:
		return "", false, nil
	case 1:
		return list.Items[0].GetName(), true, nil
	default:
		return "", false, fmt.Errorf("expected exactly one %s in %s, found at least %d", constants.KmmModulesResource, namespace, len(list.Items))
	}
}

func nodesMatchingWaitTarget(namespace, name string, ok bool) string {
	if ok {
		return formatKMMModule(namespace, name)
	}
	return fmt.Sprintf("KMM Module in namespace %s", namespace)
}

func printNodesMatchingWaitDryRun(namespace, name string, want int64, ok bool) {
	output.PrintDryRun(fmt.Sprintf(
		"Would wait up to %v for %s: %s (poll every %v)",
		constants.KmmModuleNodesMatchingWaitTimeout,
		nodesMatchingWaitTarget(namespace, name, ok),
		kmmModuleLoaderWaitConditionDescription(want),
		constants.KmmModuleNodesMatchingPollInterval,
	))
}

func printNodesMatchingWaitStart(namespace, name string, want int64) {
	output.PrintInfo(fmt.Sprintf(
		"Waiting up to %v for %s: %s (poll every %v)...",
		constants.KmmModuleNodesMatchingWaitTimeout,
		formatKMMModule(namespace, name),
		kmmModuleLoaderWaitConditionDescription(want),
		constants.KmmModuleNodesMatchingPollInterval,
	))
}

func kmmModuleLoaderWaitConditionDescription(want int64) string {
	if want == 0 {
		return fmt.Sprintf("status.moduleLoader.nodesMatchingSelectorNumber == %d", want)
	}
	return fmt.Sprintf(
		"status.moduleLoader.nodesMatchingSelectorNumber == %d and status.moduleLoader.desiredNumber == status.moduleLoader.availableNumber",
		want,
	)
}

func PrintKMMModulesInFusionAccess(mc *kube.Context) (int64, error) {
	name, ok, err := getKMMModuleName(mc, constants.FusionAccessNS)
	if err != nil {
		return 0, err
	}
	if !ok {
		nsMissing, nsErr := kmmModuleNamespaceMissing(mc, constants.FusionAccessNS)
		if nsErr != nil {
			return 0, nsErr
		}
		if nsMissing {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping %s listing (resume after namespace removed)", constants.FusionAccessNS, constants.KmmModulesResource))
			return 0, nil
		}
		output.PrintInfo(fmt.Sprintf("%s in namespace %s (oc get %s -n %s):",
			constants.KmmModulesResource, constants.FusionAccessNS, constants.KmmModulesResource, constants.FusionAccessNS))
		output.PrintInfo("  (none)")
		return 0, nil
	}

	mod, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS).Get(mc.Ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", formatKMMModule(constants.FusionAccessNS, name), err)
	}
	fin := mod.GetFinalizers()
	finDesc := "(none)"
	if len(fin) > 0 {
		finDesc = strings.Join(fin, ", ")
	}
	nodesMatching, reported, err := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldNodesMatchingSelectorNumber)
	if err != nil {
		return 0, fmt.Errorf("%s: read nodesMatchingSelectorNumber: %w", formatKMMModule(constants.FusionAccessNS, name), err)
	}
	nodesMatchingDesc := "(not reported)"
	if reported {
		nodesMatchingDesc = fmt.Sprintf("%d", nodesMatching)
	}
	output.PrintInfo(fmt.Sprintf("%s in namespace %s (oc get %s -n %s):",
		constants.KmmModulesResource, constants.FusionAccessNS, constants.KmmModulesResource, constants.FusionAccessNS))
	output.PrintInfo(fmt.Sprintf(
		"  name=%s  generation=%d  resourceVersion=%s  finalizers=[%s]  nodesMatchingSelectorNumber=%s",
		mod.GetName(), mod.GetGeneration(), mod.GetResourceVersion(), finDesc, nodesMatchingDesc,
	))
	return nodesMatching, nil
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

// PatchFusionAccessKMMModuleSelectorForMigration updates the KMM Module in ibm-fusion-access: set
// scale.spectrum.ibm.com/image-digest from node labels only if that key is not already in the selector, and
// remove kubernetes.io/arch and scale.spectrum.ibm.com/role from the Module node selector (spec.moduleLoader.selector or spec.selector).
func PatchFusionAccessKMMModuleSelectorForMigration(mc *kube.Context) error {
	moduleName, ok, err := getKMMModuleName(mc, constants.FusionAccessNS)
	if err != nil {
		return err
	}
	if !ok {
		nsMissing, nsErr := kmmModuleNamespaceMissing(mc, constants.FusionAccessNS)
		if nsErr != nil {
			return nsErr
		}
		if nsMissing {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping KMM Module selector patch (resume)", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("expected exactly one %s in %s, found 0", constants.KmmModulesResource, constants.FusionAccessNS)
	}

	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS)
	if mc.DryRun {
		obj, err := res.Get(mc.Ctx, moduleName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get %s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		_, sel, err := readKMMModuleNodeSelector(obj.Object)
		if err != nil {
			return fmt.Errorf("failed to read KMM Module node selector: %s %q: %w", constants.KmmModulesResource, moduleName, err)
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
			return fmt.Errorf("failed to get %s %q: %w", constants.KmmModulesResource, moduleName, err)
		}
		selPath, sel, err := readKMMModuleNodeSelector(obj.Object)
		if err != nil {
			return fmt.Errorf("failed to read KMM Module node selector: %s %q: %w", constants.KmmModulesResource, moduleName, err)
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
			return fmt.Errorf("failed to set selector on %s %q: %w", constants.KmmModulesResource, moduleName, err)
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
		return fmt.Errorf("failed to update %s %q: %w", constants.KmmModulesResource, moduleName, err)
	}
	return fmt.Errorf("failed to update %s %q: exhausted retries", constants.KmmModulesResource, moduleName)
}

func readModuleStatusModuleLoader(obj map[string]interface{}, field string) (n int64, reported bool, err error) {
	v, found, err := unstructured.NestedFieldNoCopy(obj, "status", "moduleLoader", field)
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
			return 0, true, fmt.Errorf("unexpected type %T for %s", v, field)
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

// ScaleDaemonNodeSelectorNodeCount reads spec.nodeSelector from the Scale Daemon CR in
// ibm-spectrum-scale and returns the number of nodes matching that selector.
// Used to determine the expected nodesMatchingSelectorNumber for the new KMM Module.
func ScaleDaemonNodeSelectorNodeCount(mc *kube.Context) (int64, error) {
	gvr, err := resolveScaleDaemonGVR(mc)
	if err != nil {
		return 0, err
	}
	list, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{Limit: 2})
	if err != nil {
		return 0, fmt.Errorf("list Scale Daemons in %s: %w", constants.SpectrumScaleNS, err)
	}
	if len(list.Items) == 0 {
		return 0, fmt.Errorf("no Scale Daemon found in %s", constants.SpectrumScaleNS)
	}
	if len(list.Items) > 1 {
		return 0, fmt.Errorf("expected exactly one Scale Daemon in %s, found at least %d", constants.SpectrumScaleNS, len(list.Items))
	}
	daemon := &list.Items[0]
	nodeSelector, found, err := unstructured.NestedStringMap(daemon.Object, "spec", "nodeSelector")
	if err != nil {
		return 0, fmt.Errorf("failed to read spec.nodeSelector on Daemon %s/%s: %w", constants.SpectrumScaleNS, daemon.GetName(), err)
	}
	if !found || len(nodeSelector) == 0 {
		return 0, fmt.Errorf("daemon %s/%s has no spec.nodeSelector", constants.SpectrumScaleNS, daemon.GetName())
	}
	parts := make([]string, 0, len(nodeSelector))
	for k, v := range nodeSelector {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	labelSelector := strings.Join(parts, ",")
	nodes, err := mc.Clientset.CoreV1().Nodes().List(mc.Ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list nodes matching Daemon nodeSelector %q: %w", labelSelector, err)
	}
	count := int64(len(nodes.Items))
	if count == 0 {
		return 0, fmt.Errorf("no nodes match Daemon %s/%s spec.nodeSelector %q", constants.SpectrumScaleNS, daemon.GetName(), labelSelector)
	}
	output.PrintInfo(fmt.Sprintf(
		"Daemon %s/%s spec.nodeSelector matches %d node(s)",
		constants.SpectrumScaleNS, daemon.GetName(), count,
	))
	return count, nil
}

func waitForKMMModuleLoaderNodesMatchingSelector(
	mc *kube.Context, namespace, moduleName string, want int64, skipIfNotFound bool, timeout, poll time.Duration,
) error {
	var loggedWaiting bool
	var loggedNotFound bool
	iter := 0
	var lastVal int64
	var lastReported bool
	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		iter++
		mod, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(namespace).Get(mc.Ctx, moduleName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if skipIfNotFound {
				output.PrintSkip(fmt.Sprintf("%s not found — treating nodesMatchingSelectorNumber wait as satisfied", formatKMMModule(namespace, moduleName)))
				return true, nil
			}
			if !loggedNotFound {
				output.PrintInfo(fmt.Sprintf("Waiting for %s to appear...", formatKMMModule(namespace, moduleName)))
				loggedNotFound = true
			}
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get %s: %w", formatKMMModule(namespace, moduleName), err)
		}
		val, reported, rerr := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldNodesMatchingSelectorNumber)
		if rerr != nil {
			return false, fmt.Errorf("%s: %w", formatKMMModule(namespace, moduleName), rerr)
		}
		lastVal = val
		lastReported = reported
		if !reported {
			if !loggedWaiting {
				output.PrintInfo(fmt.Sprintf("Waiting for %s status.moduleLoader.nodesMatchingSelectorNumber to be reported...", formatKMMModule(namespace, moduleName)))
				loggedWaiting = true
			}
			return false, nil
		}
		if val != want {
			if iter == 1 || iter%12 == 0 {
				output.PrintInfo(fmt.Sprintf(
					"Waiting for %s status.moduleLoader.nodesMatchingSelectorNumber==%d (current %d)...",
					formatKMMModule(namespace, moduleName), want, val,
				))
			}
			return false, nil
		}

		if want == 0 {
			output.PrintSuccess(fmt.Sprintf("%s: status.moduleLoader.nodesMatchingSelectorNumber is %d", formatKMMModule(namespace, moduleName), want))
			return true, nil
		}

		desired, desiredReported, rerr := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldDesiredNumber)
		if rerr != nil {
			return false, fmt.Errorf("%s: %w", formatKMMModule(namespace, moduleName), rerr)
		}
		available, availableReported, rerr := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldAvailableNumber)
		if rerr != nil {
			return false, fmt.Errorf("%s: %w", formatKMMModule(namespace, moduleName), rerr)
		}
		if !desiredReported || !availableReported {
			if iter == 1 || iter%12 == 0 {
				output.PrintInfo(fmt.Sprintf(
					"Waiting for %s status.moduleLoader.desiredNumber and status.moduleLoader.availableNumber to be reported...",
					formatKMMModule(namespace, moduleName),
				))
			}
			return false, nil
		}
		if desired != available {
			if iter == 1 || iter%12 == 0 {
				output.PrintInfo(fmt.Sprintf(
					"Waiting for %s status.moduleLoader.desiredNumber==status.moduleLoader.availableNumber (desired %d, available %d)...",
					formatKMMModule(namespace, moduleName), desired, available,
				))
			}
			return false, nil
		}

		output.PrintSuccess(fmt.Sprintf(
			"%s: status.moduleLoader.nodesMatchingSelectorNumber is %d and status.moduleLoader.desiredNumber==status.moduleLoader.availableNumber (%d)",
			formatKMMModule(namespace, moduleName), want, desired,
		))
		return true, nil
	}, timeout, poll, kmmModuleLoaderWaitPollDescription(want, formatKMMModule(namespace, moduleName)))
	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		moduleRef := formatKMMModule(namespace, moduleName)
		condition := kmmModuleLoaderWaitConditionDescription(want)
		if lastReported {
			return fmt.Errorf(
				"timeout after %v waiting for %s on %s (last reported nodesMatchingSelectorNumber %d)",
				timeout, condition, moduleRef, lastVal,
			)
		}
		return fmt.Errorf(
			"timeout after %v waiting for %s on %s (status not reported)",
			timeout, condition, moduleRef,
		)
	}
	return err
}

func kmmModuleLoaderWaitPollDescription(want int64, moduleRef string) string {
	return fmt.Sprintf("%s on %s", kmmModuleLoaderWaitConditionDescription(want), moduleRef)
}

// WaitForKMMModuleNodesMatching polls the KMM Module in namespace until
// status.moduleLoader.nodesMatchingSelectorNumber equals want. When want is not 0,
// also waits until status.moduleLoader.desiredNumber equals status.moduleLoader.availableNumber.
// In ibm-fusion-access, skips when the namespace or module is already gone.
// In ibm-spectrum-scale, the module is expected to already exist (Fusion Access
// nodesMatchingSelectorNumber reaches 0 only once the Scale module has taken over).
func WaitForKMMModuleNodesMatching(mc *kube.Context, namespace string, want int64) error {
	switch namespace {
	case constants.FusionAccessNS:
		return waitForKMMModuleNodesMatchingInFusionAccess(mc, namespace, want)
	case constants.SpectrumScaleNS:
		return waitForKMMModuleNodesMatchingInSpectrumScale(mc, namespace, want)
	default:
		return fmt.Errorf("unsupported namespace %q for KMM Module nodesMatchingSelectorNumber wait", namespace)
	}
}

func waitForKMMModuleNodesMatchingInFusionAccess(mc *kube.Context, namespace string, want int64) error {
	name, ok, err := getKMMModuleName(mc, namespace)
	if err != nil {
		return err
	}
	if !ok {
		nsMissing, nsErr := kmmModuleNamespaceMissing(mc, namespace)
		if nsErr != nil {
			return nsErr
		}
		if nsMissing {
			if mc.DryRun {
				printNodesMatchingWaitDryRun(namespace, "", want, false)
			} else {
				output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping wait for nodesMatchingSelectorNumber on KMM Module", namespace))
			}
			return nil
		}
		if mc.DryRun {
			printNodesMatchingWaitDryRun(namespace, "", want, false)
		} else {
			output.PrintSkip(fmt.Sprintf("No %s in namespace %s — skipping wait for nodesMatchingSelectorNumber", constants.KmmModulesResource, namespace))
		}
		return nil
	}
	if mc.DryRun {
		printNodesMatchingWaitDryRun(namespace, name, want, true)
		return nil
	}
	printNodesMatchingWaitStart(namespace, name, want)
	return waitForKMMModuleLoaderNodesMatchingSelector(
		mc, namespace, name, want, true,
		constants.KmmModuleNodesMatchingWaitTimeout,
		constants.KmmModuleNodesMatchingPollInterval,
	)
}

func waitForKMMModuleNodesMatchingInSpectrumScale(mc *kube.Context, namespace string, want int64) error {
	name, ok, err := getKMMModuleName(mc, namespace)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("expected exactly one %s in %s (should exist after Fusion Access module reached nodesMatchingSelectorNumber 0)", constants.KmmModulesResource, namespace)
	}
	if mc.DryRun {
		printNodesMatchingWaitDryRun(namespace, name, want, true)
		return nil
	}
	printNodesMatchingWaitStart(namespace, name, want)
	return waitForKMMModuleLoaderNodesMatchingSelector(
		mc, namespace, name, want, false,
		constants.KmmModuleNodesMatchingWaitTimeout,
		constants.KmmModuleNodesMatchingPollInterval,
	)
}

// DeleteFusionAccessKMMModuleStripFinalizers deletes the KMM Module in ibm-fusion-access and waits until
// it is removed, clearing metadata.finalizers on each poll when present so deletion is not stuck.
// Skips if the namespace is gone or the module is already absent.
func DeleteFusionAccessKMMModuleStripFinalizers(mc *kube.Context) error {
	name, ok, err := getKMMModuleName(mc, constants.FusionAccessNS)
	if err != nil {
		return err
	}
	if !ok {
		nsMissing, nsErr := kmmModuleNamespaceMissing(mc, constants.FusionAccessNS)
		if nsErr != nil {
			return nsErr
		}
		if nsMissing {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping KMM Module delete", constants.FusionAccessNS))
			return nil
		}
		output.PrintSkip(fmt.Sprintf("No %s in %s — skip delete (already removed)", constants.KmmModulesResource, constants.FusionAccessNS))
		return nil
	}

	res := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS)
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
