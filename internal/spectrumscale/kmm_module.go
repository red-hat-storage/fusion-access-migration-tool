package spectrumscale

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
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
// Used as a fallback when the old KMM Module in ibm-fusion-access is already gone (resume).
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
		"Daemon %s/%s spec.nodeSelector matches %d node(s) (fallback for missing old KMM Module)",
		constants.SpectrumScaleNS, daemon.GetName(), count,
	))
	return count, nil
}

// scaleDaemonVersionEntry represents one element of status.versions on the Scale Daemon CR.
type scaleDaemonVersionEntry struct {
	Version string
	Count   string
	Pods    string
}

// readScaleDaemonVersions reads status.versions from a Scale Daemon unstructured object.
func readScaleDaemonVersions(obj map[string]interface{}) ([]scaleDaemonVersionEntry, error) {
	raw, found, err := unstructured.NestedSlice(obj, "status", "versions")
	if err != nil {
		return nil, fmt.Errorf("read status.versions: %w", err)
	}
	if !found || len(raw) == 0 {
		return nil, nil
	}
	entries := make([]scaleDaemonVersionEntry, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("status.versions[%d]: expected map, got %T", i, item)
		}
		ver, _, _ := unstructured.NestedString(m, "version")
		cnt, _, _ := unstructured.NestedString(m, "count")
		pods, _, _ := unstructured.NestedString(m, "pods")
		entries = append(entries, scaleDaemonVersionEntry{Version: ver, Count: cnt, Pods: pods})
	}
	return entries, nil
}

func formatDaemonVersions(entries []scaleDaemonVersionEntry) string {
	if len(entries) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s(count=%s, pods=%s)", e.Version, e.Count, e.Pods))
	}
	return strings.Join(parts, ", ")
}

// WaitForScaleDaemonVersionUpgrade polls the Scale Daemon CR in ibm-spectrum-scale until
// status.versions contains only the target version (6.0.1.0) with count equal to wantCount,
// and the old version (6.0.0.2) is no longer present. This confirms the actual kernel module
// upgrade on nodes has completed, not just that nodes matched the KMM Module label selector.
func WaitForScaleDaemonVersionUpgrade(mc *kube.Context, wantCount int64) error {
	gvr, err := resolveScaleDaemonGVR(mc)
	if err != nil {
		return err
	}
	list, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{Limit: 2})
	if err != nil {
		return fmt.Errorf("list Scale Daemons in %s: %w", constants.SpectrumScaleNS, err)
	}
	if len(list.Items) == 0 {
		return fmt.Errorf("no Scale Daemon found in %s", constants.SpectrumScaleNS)
	}
	if len(list.Items) > 1 {
		return fmt.Errorf("expected exactly one Scale Daemon in %s, found at least %d", constants.SpectrumScaleNS, len(list.Items))
	}
	daemonName := list.Items[0].GetName()
	wantCountStr := strconv.FormatInt(wantCount, 10)

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would wait up to %v for Daemon %s/%s status.versions: version %s count==%s and version %s absent (poll every %v)",
			constants.KmmModuleUpgradeWaitTimeout,
			constants.SpectrumScaleNS, daemonName,
			constants.ScaleDaemonVersionTarget, wantCountStr,
			constants.ScaleDaemonVersionOld,
			constants.KmmModuleUpgradePollInterval,
		))
		return nil
	}

	output.PrintInfo(fmt.Sprintf(
		"Waiting up to %v for Daemon %s/%s: status.versions version %s count==%s and version %s absent (poll every %v)...",
		constants.KmmModuleUpgradeWaitTimeout,
		constants.SpectrumScaleNS, daemonName,
		constants.ScaleDaemonVersionTarget, wantCountStr,
		constants.ScaleDaemonVersionOld,
		constants.KmmModuleUpgradePollInterval,
	))

	res := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS)
	iter := 0
	var lastEntries []scaleDaemonVersionEntry
	err = helpers.PollUntil(mc.Ctx, func() (bool, error) {
		iter++
		daemon, err := res.Get(mc.Ctx, daemonName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get Daemon %s/%s: %w", constants.SpectrumScaleNS, daemonName, err)
		}
		entries, err := readScaleDaemonVersions(daemon.Object)
		if err != nil {
			return false, fmt.Errorf("daemon %s/%s: %w", constants.SpectrumScaleNS, daemonName, err)
		}
		lastEntries = entries

		if len(entries) == 0 {
			if iter == 1 || iter%12 == 0 {
				output.PrintInfo(fmt.Sprintf(
					"Daemon %s/%s: status.versions not reported yet...",
					constants.SpectrumScaleNS, daemonName,
				))
			}
			return false, nil
		}

		oldPresent := false
		targetFound := false
		var targetCount string
		for _, e := range entries {
			if e.Version == constants.ScaleDaemonVersionOld {
				oldPresent = true
			}
			if e.Version == constants.ScaleDaemonVersionTarget {
				targetFound = true
				targetCount = e.Count
			}
		}

		if !oldPresent && targetFound && targetCount == wantCountStr {
			output.PrintSuccess(fmt.Sprintf(
				"Daemon %s/%s: status.versions shows version %s count=%s and version %s is absent",
				constants.SpectrumScaleNS, daemonName,
				constants.ScaleDaemonVersionTarget, wantCountStr,
				constants.ScaleDaemonVersionOld,
			))
			return true, nil
		}

		if iter == 1 || iter%12 == 0 {
			output.PrintInfo(fmt.Sprintf(
				"Daemon %s/%s: status.versions=[%s] — waiting for version %s count==%s and version %s absent...",
				constants.SpectrumScaleNS, daemonName,
				formatDaemonVersions(entries),
				constants.ScaleDaemonVersionTarget, wantCountStr,
				constants.ScaleDaemonVersionOld,
			))
		}
		return false, nil
	}, constants.KmmModuleUpgradeWaitTimeout, constants.KmmModuleUpgradePollInterval,
		fmt.Sprintf("Scale Daemon %s/%s version upgrade to %s", constants.SpectrumScaleNS, daemonName, constants.ScaleDaemonVersionTarget),
	)

	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		return fmt.Errorf(
			"timeout after %v waiting for Daemon %s/%s version upgrade: want version %s count==%s and version %s absent (last seen: [%s])",
			constants.KmmModuleUpgradeWaitTimeout,
			constants.SpectrumScaleNS, daemonName,
			constants.ScaleDaemonVersionTarget, wantCountStr,
			constants.ScaleDaemonVersionOld,
			formatDaemonVersions(lastEntries),
		)
	}
	return err
}

// CheckFusionAccessKMMModuleNodesZero asserts that the KMM Module in ibm-fusion-access
// reports nodesMatchingSelectorNumber == 0. Skips gracefully if the namespace or module
// is already gone (resume scenario).
func CheckFusionAccessKMMModuleNodesZero(mc *kube.Context) error {
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
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping old KMM Module assertion", constants.FusionAccessNS))
			return nil
		}
		output.PrintSkip(fmt.Sprintf("No %s in %s — skipping old KMM Module assertion (already removed)", constants.KmmModulesResource, constants.FusionAccessNS))
		return nil
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would assert %s nodesMatchingSelectorNumber == 0",
			formatKMMModule(constants.FusionAccessNS, name),
		))
		return nil
	}
	mod, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.FusionAccessNS).Get(mc.Ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			output.PrintSkip(fmt.Sprintf("%s not found — skipping old KMM Module assertion", formatKMMModule(constants.FusionAccessNS, name)))
			return nil
		}
		return fmt.Errorf("get %s: %w", formatKMMModule(constants.FusionAccessNS, name), err)
	}
	val, reported, err := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldNodesMatchingSelectorNumber)
	if err != nil {
		return fmt.Errorf("%s: read nodesMatchingSelectorNumber: %w", formatKMMModule(constants.FusionAccessNS, name), err)
	}
	if !reported {
		output.PrintSuccess(fmt.Sprintf("%s: nodesMatchingSelectorNumber not reported (module inactive)", formatKMMModule(constants.FusionAccessNS, name)))
		return nil
	}
	if val != 0 {
		return fmt.Errorf("%s: expected nodesMatchingSelectorNumber == 0, got %d", formatKMMModule(constants.FusionAccessNS, name), val)
	}
	output.PrintSuccess(fmt.Sprintf("%s: nodesMatchingSelectorNumber is 0", formatKMMModule(constants.FusionAccessNS, name)))
	return nil
}

// CheckSpectrumScaleKMMModuleNodesMatching asserts that the KMM Module in ibm-spectrum-scale
// reports nodesMatchingSelectorNumber == want and desiredNumber == availableNumber.
func CheckSpectrumScaleKMMModuleNodesMatching(mc *kube.Context, want int64) error {
	name, ok, err := getKMMModuleName(mc, constants.SpectrumScaleNS)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("expected exactly one %s in %s, found none", constants.KmmModulesResource, constants.SpectrumScaleNS)
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would assert %s nodesMatchingSelectorNumber == %d and desiredNumber == availableNumber",
			formatKMMModule(constants.SpectrumScaleNS, name), want,
		))
		return nil
	}
	mod, err := mc.Dynamic.Resource(constants.KmmModuleGVR).Namespace(constants.SpectrumScaleNS).Get(mc.Ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get %s: %w", formatKMMModule(constants.SpectrumScaleNS, name), err)
	}
	val, reported, err := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldNodesMatchingSelectorNumber)
	if err != nil {
		return fmt.Errorf("%s: read nodesMatchingSelectorNumber: %w", formatKMMModule(constants.SpectrumScaleNS, name), err)
	}
	if !reported {
		return fmt.Errorf("%s: nodesMatchingSelectorNumber not yet reported", formatKMMModule(constants.SpectrumScaleNS, name))
	}
	if val != want {
		return fmt.Errorf("%s: expected nodesMatchingSelectorNumber == %d, got %d", formatKMMModule(constants.SpectrumScaleNS, name), want, val)
	}
	desired, desiredReported, err := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldDesiredNumber)
	if err != nil {
		return fmt.Errorf("%s: read desiredNumber: %w", formatKMMModule(constants.SpectrumScaleNS, name), err)
	}
	available, availableReported, err := readModuleStatusModuleLoader(mod.Object, moduleLoaderFieldAvailableNumber)
	if err != nil {
		return fmt.Errorf("%s: read availableNumber: %w", formatKMMModule(constants.SpectrumScaleNS, name), err)
	}
	if !desiredReported || !availableReported {
		return fmt.Errorf("%s: desiredNumber or availableNumber not yet reported", formatKMMModule(constants.SpectrumScaleNS, name))
	}
	if desired != available {
		return fmt.Errorf("%s: desiredNumber (%d) != availableNumber (%d)", formatKMMModule(constants.SpectrumScaleNS, name), desired, available)
	}
	output.PrintSuccess(fmt.Sprintf(
		"%s: nodesMatchingSelectorNumber is %d and desiredNumber == availableNumber (%d)",
		formatKMMModule(constants.SpectrumScaleNS, name), want, desired,
	))
	return nil
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
