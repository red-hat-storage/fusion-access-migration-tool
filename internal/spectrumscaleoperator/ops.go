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

	list, err := ogRes.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list OperatorGroups in %s: %w", constants.OpenShiftStorageNS, err)
	}
	for i := range list.Items {
		og := &list.Items[i]
		ok, err := cluster.OperatorGroupTargetsNamespace(og, constants.OpenShiftStorageNS)
		if err != nil {
			return err
		}
		if ok {
			output.PrintSkip(fmt.Sprintf(
				"OperatorGroup %q in %s already targets namespace %s; skipping create of %s",
				og.GetName(), constants.OpenShiftStorageNS, constants.OpenShiftStorageNS, constants.OpenShiftStorageOperatorGroupName,
			))
			return nil
		}
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
			"Would wait for FDF odf-operator CSV Succeeded in %s, enable Console plugin %q, then CSV with prefix %q in %s Succeeded",
			constants.OpenShiftStorageNS, constants.OdfConsolePlugin,
			constants.SpectrumScaleOperatorCSVNamePrefix, constants.SpectrumScaleNS,
		))
		return nil
	}
	if err := waitForFDFSubscriptionCSVSucceeded(mc, constants.OdfOperatorSubPrefix); err != nil {
		return err
	}
	if err := cluster.EnableOdfConsolePlugin(mc); err != nil {
		return fmt.Errorf("failed to enable ODF console plugin: %w", err)
	}
	return WaitForSpectrumScaleOperatorCSVAfterFDF(mc)
}

func ensureSpectrumScaleOperatorCSVReplicas(mc *kube.Context, csv *unstructured.Unstructured) error {
	csvName := csv.GetName()
	deployments, found, _ := unstructured.NestedSlice(csv.Object, "spec", "install", "spec", "deployments")
	if !found || len(deployments) == 0 {
		return fmt.Errorf("no deployments in CSV %s spec.install.spec.deployments", csvName)
	}

	needsUpdate := false
	for j, dep := range deployments {
		depMap, ok := dep.(map[string]any)
		if !ok {
			continue
		}
		replicas, replicasFound, err := unstructured.NestedInt64(depMap, "spec", "replicas")
		if err != nil {
			return fmt.Errorf("failed to read replicas in CSV %s deployment: %w", csvName, err)
		}
		name, _, _ := unstructured.NestedString(depMap, "name")
		if !replicasFound {
			return fmt.Errorf("deployment %q in CSV %s has no spec.replicas", name, csvName)
		}
		if replicas > 0 {
			continue
		}
		needsUpdate = true
		if mc.DryRun {
			continue
		}
		if err := unstructured.SetNestedField(depMap, int64(1), "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas in CSV deployment: %w", err)
		}
		deployments[j] = depMap
	}

	if !needsUpdate {
		output.PrintSkip(fmt.Sprintf("Operator deployments already running in CSV %s", csvName))
		return nil
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would scale operator deployment(s) in CSV %s to 1 replica", csvName))
		return nil
	}

	if err := unstructured.SetNestedSlice(csv.Object, deployments, "spec", "install", "spec", "deployments"); err != nil {
		return fmt.Errorf("failed to update deployments in CSV %s: %w", csvName, err)
	}
	if _, err := mc.Dynamic.Resource(constants.CsvGVR).Namespace(constants.SpectrumScaleNS).Update(mc.Ctx, csv, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CSV %s: %w", csvName, err)
	}
	output.PrintSuccess(fmt.Sprintf("Scaled up operator in CSV %s", csvName))
	return nil
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

// odfSubscriptionProvider returns the CSV provider and minor version for the odf-operator subscription.
func odfSubscriptionProvider(mc *kube.Context, sub *unstructured.Unstructured) (provider string, minor uint64, err error) {
	csvName, haveCSV := helpers.SubscriptionCurrentCSV(sub)
	if !haveCSV {
		return "", 0, nil
	}
	csv, err := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, constants.OpenShiftStorageNS, csvName)
	if err != nil {
		return "", 0, err
	}
	provider = helpers.CSVSpecProviderName(csv)
	specVersion := helpers.CSVSpecVersion(csv)
	_, minor, err = cluster.ParseFdfMajorMinor(specVersion)
	if err != nil {
		return provider, 0, err
	}
	return provider, minor, nil
}

// applyFdfCatalogAndChannelToSubscription updates a subscription's catalog source and channel to target FDF.
// The channel is set to OdfSubscriptionChannel (stable-4.21). The catalog source is set to FDFCatalogSourceName.
func applyFdfCatalogAndChannelToSubscription(dst *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(dst.Object, constants.OdfSubscriptionChannel, "spec", "channel")
	_ = unstructured.SetNestedField(dst.Object, constants.FDFCatalogSourceName, "spec", "source")
	_ = unstructured.SetNestedField(dst.Object, constants.OpenShiftMarketplaceNS, "spec", "sourceNamespace")
}

// reconcileAllOdfSubscriptions updates catalog source and channel for all subscriptions in openshift-storage
// to point at the FDF catalog. This is used when migrating from Red Hat ODF to IBM FDF.
func reconcileAllOdfSubscriptions(mc *kube.Context) error {
	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS)
	subs, err := subRes.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list subscriptions in %s: %w", constants.OpenShiftStorageNS, err)
	}

	if len(subs.Items) == 0 {
		output.PrintInfo(fmt.Sprintf("No subscriptions found in %s", constants.OpenShiftStorageNS))
		return nil
	}

	for i := range subs.Items {
		sub := &subs.Items[i]
		name := sub.GetName()

		src, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
		ch, _, _ := unstructured.NestedString(sub.Object, "spec", "channel")

		if src == constants.FDFCatalogSourceName &&
			ch == constants.OdfSubscriptionChannel {
			output.PrintSkip(fmt.Sprintf("Subscription %q already points to FDF catalog and channel", name))
			continue
		}

		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf(
				"Would update Subscription %q (source=%s→%s, channel=%s→%s)",
				name, src, constants.FDFCatalogSourceName, ch, constants.OdfSubscriptionChannel,
			))
			continue
		}

		output.PrintInfo(fmt.Sprintf(
			"Updating Subscription %q (source=%s→%s, channel=%s→%s)",
			name, src, constants.FDFCatalogSourceName, ch, constants.OdfSubscriptionChannel,
		))
		applyFdfCatalogAndChannelToSubscription(sub)
		if _, err := subRes.Update(mc.Ctx, sub, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update Subscription %q in %s: %w", name, constants.OpenShiftStorageNS, err)
		}
		output.PrintSuccess(fmt.Sprintf("Updated Subscription %q", name))
	}

	return nil
}

// waitForManualInstallPlanApproval checks all subscriptions in openshift-storage for Manual installPlanApproval.
// For each Manual subscription, it waits for the user to approve the InstallPlan before proceeding.
func waitForManualInstallPlanApproval(mc *kube.Context) error {
	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS)
	subs, err := subRes.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list subscriptions in %s: %w", constants.OpenShiftStorageNS, err)
	}

	for i := range subs.Items {
		sub := &subs.Items[i]
		name := sub.GetName()
		ipa := helpers.SubscriptionInstallPlanApproval(sub)
		if ipa != "Manual" {
			continue
		}

		output.PrintWarning(fmt.Sprintf(
			"Subscription %q has installPlanApproval=Manual; waiting for InstallPlan approval",
			name,
		))

		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf("Would wait for InstallPlan approval for Subscription %q", name))
			continue
		}

		if err := pollInstallPlanApproved(mc, name); err != nil {
			return err
		}
	}
	return nil
}

// pollInstallPlanApproved polls until the InstallPlan referenced by the named subscription is approved.
func pollInstallPlanApproved(mc *kube.Context, subName string) error {
	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS)
	ipRes := mc.Dynamic.Resource(constants.InstallPlanGVR).Namespace(constants.OpenShiftStorageNS)
	deadline := time.Now().Add(constants.InstallPlanApprovalWaitTimeout)

	return helpers.PollUntil(mc.Ctx, func() (bool, error) {
		sub, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get Subscription %s/%s: %w", constants.OpenShiftStorageNS, subName, err)
		}

		ipName, ok := helpers.SubscriptionInstallPlanRef(sub)
		if !ok {
			output.PrintInfo(fmt.Sprintf(
				"Subscription %q has no InstallPlan reference yet… (%s remaining)",
				subName, time.Until(deadline).Round(time.Second),
			))
			return false, nil
		}

		ip, err := ipRes.Get(mc.Ctx, ipName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				output.PrintInfo(fmt.Sprintf(
					"InstallPlan %q not found yet… (%s remaining)",
					ipName, time.Until(deadline).Round(time.Second),
				))
				return false, nil
			}
			return false, fmt.Errorf("get InstallPlan %s/%s: %w", constants.OpenShiftStorageNS, ipName, err)
		}

		approved, _, _ := unstructured.NestedBool(ip.Object, "spec", "approved")
		if approved {
			output.PrintSuccess(fmt.Sprintf("InstallPlan %q for Subscription %q is approved", ipName, subName))
			return true, nil
		}

		output.PrintWarning(fmt.Sprintf(
			"InstallPlan %q for Subscription %q requires manual approval. "+
				"Please approve it via: oc patch installplan %s -n %s --type merge -p '{\"spec\":{\"approved\":true}}' (%s remaining)",
			ipName, subName, ipName, constants.OpenShiftStorageNS,
			time.Until(deadline).Round(time.Second),
		))
		return false, nil
	}, constants.InstallPlanApprovalWaitTimeout, constants.InstallPlanApprovalPollInterval,
		fmt.Sprintf("InstallPlan approved for Subscription %s", subName))
}

// CreateFDFSubscriptionAndWait ensures the OperatorGroup and odf-operator Subscription match the FDF install manifest.
// It supports three scenarios:
//   - No odf-operator subscription exists: creates a new FDF subscription.
//   - Red Hat ODF 4.20/4.21 is installed: migrates all subscriptions in openshift-storage to FDF catalog.
//   - IBM FDF 4.20 is installed: updates channel and catalog source on odf-operator only.
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

	existing, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
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

	provider, minor, provErr := odfSubscriptionProvider(mc, existing)
	if provErr != nil {
		output.PrintWarning(fmt.Sprintf("Could not determine odf-operator provider: %v; falling back to FDF reconcile path", provErr))
		return reconcileExistingFdfSubscriptionAndWait(mc, subRes)
	}

	if provider == constants.OdfProviderRedHat {
		output.PrintInfo(fmt.Sprintf("Red Hat ODF %d.%d detected — migrating all subscriptions in %s to IBM FDF catalog",
			constants.RequiredFDFMajor, minor, constants.OpenShiftStorageNS))
		return reconcileOdfToFdfAndWait(mc)
	}

	return reconcileExistingFdfSubscriptionAndWait(mc, subRes)
}

// reconcileOdfToFdfAndWait handles Red Hat ODF -> IBM FDF migration by updating all subscriptions
// in openshift-storage to point at the FDF catalog, then waiting for CSVs.
func reconcileOdfToFdfAndWait(mc *kube.Context) error {
	if err := reconcileAllOdfSubscriptions(mc); err != nil {
		return err
	}
	if err := waitForManualInstallPlanApproval(mc); err != nil {
		return err
	}
	return WaitForFDFAndSpectrumScaleOperatorCSVs(mc)
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

	if err := waitForManualInstallPlanApproval(mc); err != nil {
		return err
	}
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
				if err := ensureSpectrumScaleOperatorCSVReplicas(mc, item); err != nil {
					return false, err
				}
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
