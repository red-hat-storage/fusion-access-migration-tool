package spectrumscale

import (
	"fmt"
	"maps"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const (
	fusionLabelOwnedByFSCName      = "fusion.storage.openshift.io/owned-by-fsc-name"
	fusionLabelOwnedByFSCNamespace = "fusion.storage.openshift.io/owned-by-fsc-namespace"

	metadataRemovalVerifyTimeout    = 90 * time.Second
	metadataRemovalOuterPoll        = 2 * time.Second
	metadataRemovalPostWriteTimeout = 25 * time.Second
	metadataRemovalPostWritePoll    = 500 * time.Millisecond
	metadataRemovalMutateMaxTries   = 12
)

// clearOwnerRefsMergePatch clears metadata.ownerReferences (JSON merge patch).
var clearOwnerRefsMergePatch = []byte(`{"metadata":{"ownerReferences":[]}}`)

// clearFusionOwnedByLabelsMergePatch removes only the fusion owned-by labels (null deletes keys in merge patch).
var clearFusionOwnedByLabelsMergePatch = []byte(
	`{"metadata":{"labels":{` +
		`"` + fusionLabelOwnedByFSCName + `":null,` +
		`"` + fusionLabelOwnedByFSCNamespace + `":null` +
		`}}}`,
)

func fusionOwnedByLabelsPresent(labels map[string]string) bool {
	if labels == nil {
		return false
	}
	_, a := labels[fusionLabelOwnedByFSCName]
	_, b := labels[fusionLabelOwnedByFSCNamespace]
	return a || b
}

func clearOwnerReferencesOnce(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
) error {
	for try := 0; try < metadataRemovalMutateMaxTries; try++ {
		latest, err := getUnstructuredWithRetries(mc, res, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if len(latest.GetOwnerReferences()) == 0 {
			return nil
		}
		latest.SetOwnerReferences(nil)
		_, uerr := res.Update(mc.Ctx, latest, metav1.UpdateOptions{})
		if uerr == nil {
			return nil
		}
		if apierrors.IsConflict(uerr) {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		_, perr := res.Patch(mc.Ctx, name, types.MergePatchType, clearOwnerRefsMergePatch, metav1.PatchOptions{})
		if perr == nil {
			return nil
		}
		if apierrors.IsConflict(perr) {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return fmt.Errorf("clear ownerReferences: update: %w; merge patch: %v", uerr, perr)
	}
	return fmt.Errorf("clear ownerReferences: exhausted %d conflict retries on %q", metadataRemovalMutateMaxTries, name)
}

func pollUntilOwnerRefsGone(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
	deadline time.Time,
) (lastCount int, ok bool, err error) {
	for time.Now().Before(deadline) {
		cur, gerr := getUnstructuredWithRetries(mc, res, name)
		if gerr != nil {
			if apierrors.IsNotFound(gerr) {
				return 0, true, nil
			}
			return 0, false, gerr
		}
		lastCount = len(cur.GetOwnerReferences())
		if lastCount == 0 {
			return 0, true, nil
		}
		time.Sleep(metadataRemovalPostWritePoll)
	}
	return lastCount, false, nil
}

func removeOwnerRefsWithRetryAndVerify(
	mc *kube.Context,
	gvr schema.GroupVersionResource,
	namespace string,
	item *unstructured.Unstructured,
) error {
	name := item.GetName()
	res := mc.Dynamic.Resource(gvr).Namespace(namespace)
	overallDeadline := time.Now().Add(metadataRemovalVerifyTimeout)
	var loggedRace bool

	for time.Now().Before(overallDeadline) {
		cur, err := getUnstructuredWithRetries(mc, res, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("%s/%s: %w", namespace, name, err)
		}
		if len(cur.GetOwnerReferences()) == 0 {
			return nil
		}

		if err := clearOwnerReferencesOnce(mc, res, name); err != nil {
			return fmt.Errorf("%s/%s: %w", namespace, name, err)
		}

		verifyUntil := time.Now().Add(metadataRemovalPostWriteTimeout)
		lastCount, ok, perr := pollUntilOwnerRefsGone(mc, res, name, verifyUntil)
		if perr != nil {
			return fmt.Errorf("%s/%s: verify poll: %w", namespace, name, perr)
		}
		if ok {
			return nil
		}

		if !loggedRace {
			output.PrintInfo(fmt.Sprintf(
				"ownerReferences on %s/%s not yet stable after write (still %d); another controller may be racing — will retry for up to %v",
				namespace, name, lastCount, time.Until(overallDeadline).Round(time.Second),
			))
			loggedRace = true
		}
		time.Sleep(metadataRemovalOuterPoll)
	}

	last, lastErr := getUnstructuredWithRetries(mc, res, name)
	n := 0
	if lastErr == nil && last != nil {
		n = len(last.GetOwnerReferences())
	}
	if lastErr != nil {
		return fmt.Errorf(
			"%s/%s: timeout after %v — could not confirm ownerReferences (last get: %w); check the object with oc get",
			namespace, name, metadataRemovalVerifyTimeout, lastErr,
		)
	}
	return fmt.Errorf(
		"%s/%s: timeout after %v — ownerReferences still present (count=%d); if removal works in the cluster, scale down the controller that restores ownerReferences and re-run",
		namespace, name, metadataRemovalVerifyTimeout, n,
	)
}

func clearFusionLabelsOnce(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
) error {
	for try := 0; try < metadataRemovalMutateMaxTries; try++ {
		latest, err := getUnstructuredWithRetries(mc, res, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !fusionOwnedByLabelsPresent(latest.GetLabels()) {
			return nil
		}
		_, perr := res.Patch(mc.Ctx, name, types.MergePatchType, clearFusionOwnedByLabelsMergePatch, metav1.PatchOptions{})
		if perr == nil {
			return nil
		}
		if !apierrors.IsConflict(perr) {
			labels := latest.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels = maps.Clone(labels)
			delete(labels, fusionLabelOwnedByFSCName)
			delete(labels, fusionLabelOwnedByFSCNamespace)
			latest.SetLabels(labels)
			_, uerr := res.Update(mc.Ctx, latest, metav1.UpdateOptions{})
			if uerr == nil {
				return nil
			}
			if apierrors.IsConflict(uerr) {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			return fmt.Errorf("clear fusion labels: patch: %v; update: %w", perr, uerr)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("clear fusion labels: exhausted %d conflict retries on %q", metadataRemovalMutateMaxTries, name)
}

func pollUntilFusionLabelsGone(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
	deadline time.Time,
) (stillPresent bool, err error) {
	for time.Now().Before(deadline) {
		cur, gerr := getUnstructuredWithRetries(mc, res, name)
		if gerr != nil {
			if apierrors.IsNotFound(gerr) {
				return false, nil
			}
			return false, gerr
		}
		if !fusionOwnedByLabelsPresent(cur.GetLabels()) {
			return false, nil
		}
		time.Sleep(metadataRemovalPostWritePoll)
	}
	return true, nil
}

func removeFusionLabelsWithRetryAndVerify(
	mc *kube.Context,
	gvr schema.GroupVersionResource,
	namespace string,
	item *unstructured.Unstructured,
) error {
	name := item.GetName()
	res := mc.Dynamic.Resource(gvr).Namespace(namespace)
	overallDeadline := time.Now().Add(metadataRemovalVerifyTimeout)
	var loggedRace bool

	for time.Now().Before(overallDeadline) {
		cur, err := getUnstructuredWithRetries(mc, res, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("%s/%s: %w", namespace, name, err)
		}
		if !fusionOwnedByLabelsPresent(cur.GetLabels()) {
			return nil
		}

		if err := clearFusionLabelsOnce(mc, res, name); err != nil {
			return fmt.Errorf("%s/%s: %w", namespace, name, err)
		}

		verifyUntil := time.Now().Add(metadataRemovalPostWriteTimeout)
		still, perr := pollUntilFusionLabelsGone(mc, res, name, verifyUntil)
		if perr != nil {
			return fmt.Errorf("%s/%s: verify poll: %w", namespace, name, perr)
		}
		if !still {
			return nil
		}

		if !loggedRace {
			output.PrintInfo(fmt.Sprintf(
				"fusion owned-by labels on %s/%s not yet stable after write; another controller may be racing — will retry for up to %v",
				namespace, name, time.Until(overallDeadline).Round(time.Second),
			))
			loggedRace = true
		}
		time.Sleep(metadataRemovalOuterPoll)
	}

	return fmt.Errorf(
		"%s/%s: timeout after %v — fusion owned-by labels still present; if removal works in the cluster, scale down the controller that restores those labels and re-run",
		namespace, name, metadataRemovalVerifyTimeout,
	)
}

func RemoveOwnerRefsAndLabels(mc *kube.Context) error {
	for _, resource := range []string{constants.LocalDiskResource, constants.FilesystemResource} {
		if err := processResources(mc, resource); err != nil {
			return fmt.Errorf("failed to process %s: %w", resource, err)
		}
	}
	return nil
}

func processResources(mc *kube.Context, resourceType string) error {
	output.PrintInfo(fmt.Sprintf("Processing %s resources...", resourceType))

	gvr := helpers.ParseGVR(resourceType)
	list, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		output.PrintSkip(fmt.Sprintf("No %s resources found in namespace %s", resourceType, constants.SpectrumScaleNS))
		return nil
	}

	if len(list.Items) == 0 {
		output.PrintSkip(fmt.Sprintf("No %s resources found in namespace %s", resourceType, constants.SpectrumScaleNS))
		return nil
	}

	processed := 0
	modifiedRefs := 0
	modifiedLabels := 0
	skipped := 0
	failed := 0

	for _, item := range list.Items {
		processed++
		resourceModified := false

		if len(item.GetOwnerReferences()) > 0 {
			refs := append([]metav1.OwnerReference(nil), item.GetOwnerReferences()...)
			detail := formatOwnerRefsForLog(refs)
			if mc.DryRun {
				output.PrintDryRun(fmt.Sprintf(
					"Would remove ownerReferences from %s/%s: %s",
					resourceType, item.GetName(), detail,
				))
				modifiedRefs++
				resourceModified = true
			} else {
				if err := removeOwnerRefsWithRetryAndVerify(mc, gvr, constants.SpectrumScaleNS, &item); err != nil {
					output.PrintError(fmt.Sprintf(
						"Failed to remove ownerReferences from %s/%s (was: %s): %v",
						resourceType, item.GetName(), detail, err,
					))
					failed++
				} else {
					output.PrintSuccess(fmt.Sprintf(
						"Removed ownerReferences from %s/%s: %s",
						resourceType, item.GetName(), detail,
					))
					modifiedRefs++
					resourceModified = true
				}
			}
		}

		hasFusionLabels := fusionOwnedByLabelsPresent(item.GetLabels())

		if hasFusionLabels {
			if mc.DryRun {
				output.PrintDryRun(fmt.Sprintf("Would remove fusion labels from %s/%s", resourceType, item.GetName()))
				modifiedLabels++
				resourceModified = true
			} else {
				if err := removeFusionLabelsWithRetryAndVerify(mc, gvr, constants.SpectrumScaleNS, &item); err != nil {
					output.PrintError(fmt.Sprintf("Failed to remove fusion labels from %s/%s: %v", resourceType, item.GetName(), err))
					failed++
				} else {
					output.PrintSuccess(fmt.Sprintf("Removed fusion labels from %s/%s", resourceType, item.GetName()))
					modifiedLabels++
					resourceModified = true
				}
			}
		}

		if !resourceModified {
			skipped++
		}
	}

	output.PrintInfo(fmt.Sprintf("Summary for %s:", resourceType))
	output.PrintInfo(fmt.Sprintf("  Total resources: %d", processed))
	if mc.DryRun {
		output.PrintInfo(fmt.Sprintf("  Would remove ownerReferences: %s%d%s", output.ColorGreen, modifiedRefs, output.ColorReset))
		output.PrintInfo(fmt.Sprintf("  Would remove fusion labels: %s%d%s", output.ColorGreen, modifiedLabels, output.ColorReset))
	} else {
		output.PrintInfo(fmt.Sprintf("  Removed ownerReferences: %s%d%s", output.ColorGreen, modifiedRefs, output.ColorReset))
		output.PrintInfo(fmt.Sprintf("  Removed fusion labels: %s%d%s", output.ColorGreen, modifiedLabels, output.ColorReset))
	}
	output.PrintInfo(fmt.Sprintf("  Skipped (nothing to remove): %s%d%s", output.ColorYellow, skipped, output.ColorReset))
	if failed > 0 {
		output.PrintInfo(fmt.Sprintf("  Failed: %s%d%s", output.ColorRed, failed, output.ColorReset))
		return fmt.Errorf("failed to process %d resources", failed)
	}

	return nil
}

func RemoveFinalizersFromFilesystemClaims(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.FilesystemClaimResource)
	list, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystemclaims: %w", err)
	}

	for _, claim := range list.Items {
		if len(claim.GetFinalizers()) == 0 {
			continue
		}
		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf("Would remove finalizers from filesystemclaim %s", claim.GetName()))
			continue
		}
		claim.SetFinalizers([]string{})
		if _, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).Update(mc.Ctx, &claim, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to remove finalizers from %s: %w", claim.GetName(), err)
		}
		output.PrintSuccess(fmt.Sprintf("Removed finalizers from filesystemclaim %s", claim.GetName()))
	}
	return nil
}
