// Package spectrumscaleoperator — ibm-spectrum-scale-operator and openshift-storage OLM steps.
package spectrumscaleoperator

import (
	"fmt"
	"strings"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

func ScaleDownSpectrumScaleOperator(mc *kube.Context) error {
	if _, nsErr := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleOperatorNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping Scale operator deployment scaledown", constants.SpectrumScaleOperatorNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", constants.SpectrumScaleOperatorNS, nsErr)
	}

	deployment, err := mc.Clientset.AppsV1().Deployments(constants.SpectrumScaleOperatorNS).Get(
		mc.Ctx, constants.SpectrumScaleController, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		output.PrintSkip(fmt.Sprintf("Deployment %s not found", constants.SpectrumScaleController))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", constants.SpectrumScaleController, err)
	}
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		output.PrintSkip(fmt.Sprintf("%s already scaled to 0", constants.SpectrumScaleController))
		return nil
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would scale down %s to 0 replicas", constants.SpectrumScaleController))
		return nil
	}

	replicas := int32(0)
	deployment.Spec.Replicas = &replicas
	if _, err := mc.Clientset.AppsV1().Deployments(constants.SpectrumScaleOperatorNS).Update(mc.Ctx, deployment, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to scale down %s: %w", constants.SpectrumScaleController, err)
	}
	output.PrintSuccess(fmt.Sprintf("Scaled down %s to 0 replicas", constants.SpectrumScaleController))
	return nil
}

func ensureOpenShiftStorageOperatorGroup(mc *kube.Context) error {
	ogRes := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.OpenShiftStorageNS)
	_, err := ogRes.Get(mc.Ctx, constants.OpenShiftStorageOperatorGroupName, metav1.GetOptions{})
	if err == nil {
		output.PrintSkip(fmt.Sprintf("OperatorGroup %s already exists in %s", constants.OpenShiftStorageOperatorGroupName, constants.OpenShiftStorageNS))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get OperatorGroup %s in %s: %w", constants.OpenShiftStorageOperatorGroupName, constants.OpenShiftStorageNS, err)
	}

	og := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      constants.OpenShiftStorageOperatorGroupName,
				"namespace": constants.OpenShiftStorageNS,
			},
			"spec": map[string]interface{}{
				"targetNamespaces": []interface{}{constants.OpenShiftStorageNS},
				"upgradeStrategy":  "Default",
			},
		},
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would create OperatorGroup %s in %s (upgradeStrategy=Default)", constants.OpenShiftStorageOperatorGroupName, constants.OpenShiftStorageNS))
		return nil
	}

	_, err = ogRes.Create(mc.Ctx, og, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create OperatorGroup %s: %w", constants.OpenShiftStorageOperatorGroupName, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("OperatorGroup %s already exists", constants.OpenShiftStorageOperatorGroupName))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created OperatorGroup %s", constants.OpenShiftStorageOperatorGroupName))
	}
	return nil
}

// WaitForFDFAndSpectrumScaleOperatorCSVs waits for the odf-operator subscription CSV in openshift-storage to succeed,
// then for the IBM Spectrum Scale operator CSV in ibm-spectrum-scale.
func WaitForFDFAndSpectrumScaleOperatorCSVs(mc *kube.Context) error {
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would wait for FDF odf-operator CSV Succeeded in %s, then CSV with prefix %q in %s Succeeded",
			constants.OpenShiftStorageNS, constants.SpectrumScaleOperatorCSVNamePrefix, constants.SpectrumScaleNS,
		))
		return nil
	}
	if err := waitForFDFSubscriptionCSVSucceeded(mc, constants.OdfOperatorSubPrefix); err != nil {
		return err
	}
	return WaitForSpectrumScaleOperatorCSVAfterFDF(mc)
}

func waitForFDFSubscriptionCSVSucceeded(mc *kube.Context, subName string) error {
	output.PrintInfo("Waiting for FDF CSV to reach Succeeded phase (up to 10 minutes)...")

	return helpers.PollUntil(mc.Ctx, func() (bool, error) {
		sub, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS).Get(
			mc.Ctx, subName, metav1.GetOptions{},
		)
		if err != nil {
			return false, nil
		}
		csvName, ok := helpers.SubscriptionCurrentCSV(sub)
		if !ok {
			return false, nil
		}
		csv, err := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, constants.OpenShiftStorageNS, csvName)
		if err != nil {
			return false, nil
		}
		phase := helpers.CSVStatusPhase(csv)
		if phase == "Succeeded" {
			provider := helpers.CSVSpecProviderName(csv)
			output.PrintSuccess(fmt.Sprintf("FDF CSV %s is ready (provider: %s)", csvName, provider))
			return true, nil
		}
		output.PrintInfo(fmt.Sprintf("Waiting for FDF CSV %s (current phase: %s)...", csvName, phase))
		return false, nil
	}, 10*time.Minute, 15*time.Second, "FDF CSV Succeeded phase")
}

func newFdfOperatorSubscription() *unstructured.Unstructured {
	subName := constants.OdfOperatorSubPrefix
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      subName,
				"namespace": constants.OpenShiftStorageNS,
				"labels": map[string]interface{}{
					constants.OdfOperatorCreatorFusionLabelKey: "",
				},
			},
			"spec": map[string]interface{}{
				"channel":             constants.OdfSubscriptionChannel,
				"name":                constants.OdfOperatorSubPrefix,
				"source":              constants.FDFCatalogSourceName,
				"sourceNamespace":     constants.OpenShiftMarketplaceNS,
				"installPlanApproval": "Automatic",
			},
		},
	}
}

func fdfSubscriptionSpecMatchesDesired(sub *unstructured.Unstructured) bool {
	ch, _, _ := unstructured.NestedString(sub.Object, "spec", "channel")
	src, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
	srcNS, _, _ := unstructured.NestedString(sub.Object, "spec", "sourceNamespace")
	ipa, _, _ := unstructured.NestedString(sub.Object, "spec", "installPlanApproval")
	name, _, _ := unstructured.NestedString(sub.Object, "spec", "name")
	lab, labelOK, _ := unstructured.NestedString(sub.Object, "metadata", "labels", constants.OdfOperatorCreatorFusionLabelKey)
	if ch != constants.OdfSubscriptionChannel ||
		src != constants.FDFCatalogSourceName ||
		srcNS != constants.OpenShiftMarketplaceNS ||
		ipa != "Automatic" ||
		name != constants.OdfOperatorSubPrefix {
		return false
	}
	return labelOK && lab == ""
}

func applyDesiredFdfSubscriptionFields(dst *unstructured.Unstructured) {
	labels, _, _ := unstructured.NestedStringMap(dst.Object, "metadata", "labels")
	if labels == nil {
		labels = map[string]string{}
	}
	labels[constants.OdfOperatorCreatorFusionLabelKey] = ""
	m := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		m[k] = v
	}
	_ = unstructured.SetNestedMap(dst.Object, m, "metadata", "labels")
	_ = unstructured.SetNestedField(dst.Object, constants.OdfSubscriptionChannel, "spec", "channel")
	_ = unstructured.SetNestedField(dst.Object, constants.OdfOperatorSubPrefix, "spec", "name")
	_ = unstructured.SetNestedField(dst.Object, constants.FDFCatalogSourceName, "spec", "source")
	_ = unstructured.SetNestedField(dst.Object, constants.OpenShiftMarketplaceNS, "spec", "sourceNamespace")
	_ = unstructured.SetNestedField(dst.Object, "Automatic", "spec", "installPlanApproval")
}

func ibmCsvMinorFromSubscription(mc *kube.Context, sub *unstructured.Unstructured) (minor uint64, ok bool, err error) {
	csvName, haveCSV := helpers.SubscriptionCurrentCSV(sub)
	if !haveCSV {
		return 0, false, nil
	}
	csv, err := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, constants.OpenShiftStorageNS, csvName)
	if err != nil {
		return 0, false, err
	}
	if helpers.CSVSpecProviderName(csv) != constants.OdfProviderIBM {
		return 0, false, nil
	}
	specVersion := helpers.CSVSpecVersion(csv)
	_, minor, err = cluster.ParseFdfMajorMinor(specVersion)
	if err != nil {
		return 0, false, err
	}
	return minor, true, nil
}

// CreateFDFSubscriptionAndWait ensures the OperatorGroup and odf-operator Subscription match the FDF install manifest.
// If IBM FDF 4.20.x is already installed, it updates channel and catalog source instead of creating the Subscription.
func CreateFDFSubscriptionAndWait(mc *kube.Context) error {
	if err := cluster.EnsureNamespace(mc, constants.OpenShiftStorageNS); err != nil {
		return err
	}
	if err := ensureOpenShiftStorageOperatorGroup(mc); err != nil {
		return err
	}

	subName := constants.OdfOperatorSubPrefix
	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS)

	output.PrintInfo(fmt.Sprintf(
		"Reconciling odf-operator Subscription in %s (channel=%s source=%s sourceNamespace=%s)",
		constants.OpenShiftStorageNS, constants.OdfSubscriptionChannel,
		constants.FDFCatalogSourceName, constants.OpenShiftMarketplaceNS,
	))

	_, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get Subscription %s/%s: %w", constants.OpenShiftStorageNS, subName, err)
	}

	if apierrors.IsNotFound(err) {
		fdfSubscription := newFdfOperatorSubscription()
		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf(
				"Would create FDF subscription %s in %s (channel %s, source %s, sourceNamespace %s)",
				subName, constants.OpenShiftStorageNS, constants.OdfSubscriptionChannel,
				constants.FDFCatalogSourceName, constants.OpenShiftMarketplaceNS,
			))
			return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
		}
		_, err = subRes.Create(mc.Ctx, fdfSubscription, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create FDF subscription: %w", err)
		}
		if apierrors.IsAlreadyExists(err) {
			output.PrintSkip(fmt.Sprintf("Subscription %s already exists", subName))
			return reconcileExistingFdfSubscriptionAndWait(mc, subRes)
		}
		output.PrintSuccess(fmt.Sprintf("Created FDF subscription %s", subName))
		return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
	}

	return reconcileExistingFdfSubscriptionAndWait(mc, subRes)
}

func reconcileExistingFdfSubscriptionAndWait(mc *kube.Context, subRes dynamic.ResourceInterface) error {
	subName := constants.OdfOperatorSubPrefix
	existing, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Subscription %s/%s: %w", constants.OpenShiftStorageNS, subName, err)
	}

	if mc.DryRun {
		if fdfSubscriptionSpecMatchesDesired(existing) {
			output.PrintDryRun(fmt.Sprintf("Would skip update; Subscription %s already matches desired channel and source", subName))
		} else {
			output.PrintDryRun(fmt.Sprintf(
				"Would update Subscription %s (channel, source, sourceNamespace, labels)",
				subName,
			))
		}
		return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
	}

	if fdfSubscriptionSpecMatchesDesired(existing) {
		output.PrintSkip(fmt.Sprintf("Subscription %s already matches desired channel and catalog source", subName))
		return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
	}

	minor, csvOK, err := ibmCsvMinorFromSubscription(mc, existing)
	if err != nil {
		return fmt.Errorf("inspect FDF CSV for subscription: %w", err)
	}
	if csvOK && minor == 20 {
		output.PrintInfo("Existing IBM FDF 4.20 detected — updating Subscription channel and catalog source.")
	} else {
		output.PrintInfo(fmt.Sprintf("Updating Subscription %s to desired channel and catalog source.", subName))
	}

	applyDesiredFdfSubscriptionFields(existing)
	_, err = subRes.Update(mc.Ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update FDF subscription %s: %w", subName, err)
	}
	output.PrintSuccess(fmt.Sprintf("Updated FDF subscription %s", subName))
	return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
}

// WaitForSpectrumScaleOperatorCSVAfterFDF waits for a CSV named ibm-spectrum-scale-operator.* in ibm-spectrum-scale to succeed.
func WaitForSpectrumScaleOperatorCSVAfterFDF(mc *kube.Context) error {
	const wait = 15 * time.Minute
	output.PrintInfo(fmt.Sprintf(
		"Waiting for IBM Spectrum Scale operator CSV (prefix %q) in %s to reach Succeeded (up to %v)...",
		constants.SpectrumScaleOperatorCSVNamePrefix, constants.SpectrumScaleNS, wait,
	))

	return helpers.PollUntil(mc.Ctx, func() (bool, error) {
		list, err := mc.Dynamic.Resource(constants.CsvGVR).Namespace(constants.SpectrumScaleNS).List(
			mc.Ctx, metav1.ListOptions{},
		)
		if err != nil {
			return false, nil
		}

		var waitingName, waitingPhase string
		foundAny := false
		for i := range list.Items {
			item := &list.Items[i]
			name := item.GetName()
			if !strings.HasPrefix(name, constants.SpectrumScaleOperatorCSVNamePrefix) {
				continue
			}
			phase := helpers.CSVStatusPhase(item)
			if phase == "Succeeded" {
				output.PrintSuccess(fmt.Sprintf(
					"Spectrum Scale operator CSV %s is ready in %s (phase Succeeded)",
					name, constants.SpectrumScaleNS,
				))
				return true, nil
			}
			foundAny = true
			waitingName = name
			waitingPhase = phase
		}

		if !foundAny {
			output.PrintInfo(fmt.Sprintf(
				"No CSV with prefix %q in %s yet...",
				constants.SpectrumScaleOperatorCSVNamePrefix, constants.SpectrumScaleNS,
			))
			return false, nil
		}
		if waitingPhase == "" {
			waitingPhase = "Unknown"
		}
		output.PrintInfo(fmt.Sprintf(
			"Waiting for CSV %s in %s (current phase: %s)...",
			waitingName, constants.SpectrumScaleNS, waitingPhase,
		))
		return false, nil
	}, wait, 15*time.Second, fmt.Sprintf("CSV prefix %q in %s Succeeded", constants.SpectrumScaleOperatorCSVNamePrefix, constants.SpectrumScaleNS))
}
