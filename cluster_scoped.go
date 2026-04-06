package main

import (
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

// --- Validation operations (cluster-scoped reads) ---

func validateClusterConnectivity(mc *MigrationContext) error {
	version, err := mc.clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("not logged in to OpenShift cluster: %w", err)
	}

	config, _ := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	currentContext := config.CurrentContext

	printInfo(fmt.Sprintf("Cluster: %s", currentContext))
	if version != nil {
		printInfo(fmt.Sprintf("Server version: %s", version.GitVersion))
	}
	printInfo(fmt.Sprintf("Namespaces: %s, %s, %s, %s", fusionAccessNS, spectrumScaleNS, spectrumScaleOperatorNS, openShiftStorageNS))
	printSuccess("Cluster connectivity verified")
	return nil
}

func validateOCPVersion(mc *MigrationContext) error {
	cv, err := mc.dynamicClient.Resource(clusterVersionGVR).Get(mc.ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterVersion: %w", err)
	}
	histories, found, _ := unstructured.NestedSlice(cv.Object, "status", "history")
	if !found || len(histories) == 0 {
		return fmt.Errorf("no version history found in ClusterVersion")
	}
	entry, ok := histories[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected format in ClusterVersion history")
	}
	currentVersion, _, _ := unstructured.NestedString(entry, "version")
	if !strings.HasPrefix(currentVersion, requiredOCPVersion) {
		return fmt.Errorf("OCP version %s does not match required %s.x", currentVersion, requiredOCPVersion)
	}
	printSuccess(fmt.Sprintf("OCP version %s meets requirement (%s.x)", currentVersion, requiredOCPVersion))
	return nil
}

func validateExistingInstalls(mc *MigrationContext) error {
	_, nsErr := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, openShiftStorageNS, metav1.GetOptions{})
	if nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			printInfo(fmt.Sprintf("Namespace %s not present; skipping ODF/FDF subscription checks (created when installing FDF)", openShiftStorageNS))
			printSuccess("No conflicting FDF installation found")
			return nil
		}
		return fmt.Errorf("failed to get namespace %s: %w", openShiftStorageNS, nsErr)
	}

	odfSubs, _ := mc.dynamicClient.Resource(subscriptionGVR).Namespace(openShiftStorageNS).List(mc.ctx, metav1.ListOptions{})
	if odfSubs != nil {
		for _, sub := range odfSubs.Items {
			if strings.HasPrefix(sub.GetName(), odfOperatorSubPrefix) {
				printInfo(fmt.Sprintf("Found existing ODF subscription: %s", sub.GetName()))
			}
		}
	}

	fdfSubs, _ := mc.dynamicClient.Resource(subscriptionGVR).Namespace(openShiftStorageNS).List(mc.ctx, metav1.ListOptions{})
	if fdfSubs != nil {
		for _, sub := range fdfSubs.Items {
			if !strings.HasPrefix(sub.GetName(), odfOperatorSubPrefix) {
				continue
			}
			csvName, found, _ := unstructured.NestedString(sub.Object, "status", "currentCSV")
			if !found || csvName == "" {
				continue
			}
			csv, err := mc.dynamicClient.Resource(csvGVR).Namespace(openShiftStorageNS).Get(mc.ctx, csvName, metav1.GetOptions{})
			if err != nil {
				continue
			}
			provider, _, _ := unstructured.NestedString(csv.Object, "spec", "provider", "name")
			if provider == odfProviderIBM {
				return fmt.Errorf(
					"FDF (IBM provider) already installed in %s — preflight blocked; use --continue to skip preflight and resume migration",
					openShiftStorageNS,
				)
			}
		}
	}

	printSuccess("No conflicting FDF installation found")
	return nil
}

// discoverFDFCatalogSourceName picks a CatalogSource in openshift-marketplace whose
// name contains "fusion" or "fdf" (case-insensitive). If several match, prefers a
// name containing "fdf", otherwise the first name after sorting for stability.
func discoverFDFCatalogSourceName(mc *MigrationContext) (string, error) {
	list, err := mc.dynamicClient.Resource(catalogSourceGVR).Namespace(openShiftMarketplaceNS).List(
		mc.ctx, metav1.ListOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("list CatalogSources in %s: %w", openShiftMarketplaceNS, err)
	}

	var candidates []string
	for _, cat := range list.Items {
		n := cat.GetName()
		nl := strings.ToLower(n)
		if strings.Contains(nl, "fusion") || strings.Contains(nl, "fdf") {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"no CatalogSource with \"fusion\" or \"fdf\" in its name under namespace %s",
			openShiftMarketplaceNS,
		)
	}

	sort.Strings(candidates)
	chosen := candidates[0]
	for _, n := range candidates {
		if strings.Contains(strings.ToLower(n), "fdf") {
			chosen = n
			break
		}
	}

	if len(candidates) > 1 {
		printInfo(fmt.Sprintf(
			"Multiple FDF/Fusion CatalogSources in %s: %s — using %q for Subscription spec.source",
			openShiftMarketplaceNS, strings.Join(candidates, ", "), chosen,
		))
	}
	return chosen, nil
}

func validateCatalogAvailability(mc *MigrationContext) error {
	name, err := discoverFDFCatalogSourceName(mc)
	if err != nil {
		return fmt.Errorf("FDF catalog source: %w", err)
	}
	printInfo(fmt.Sprintf(
		"FDF CatalogSource for odf-operator Subscription (spec.source): %s (namespace %s)",
		name, openShiftMarketplaceNS,
	))
	printSuccess("Fusion/FDF catalog source available")
	return nil
}

func validateRequiredNamespaces(mc *MigrationContext) error {
	_, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, spectrumScaleNS, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace '%s' does not exist: %w", spectrumScaleNS, err)
	}

	_, err = mc.clientset.CoreV1().Namespaces().Get(mc.ctx, spectrumScaleOperatorNS, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"namespace %q not found — preflight requires it for a fresh migration; use --continue to skip preflight if resuming after removal",
				spectrumScaleOperatorNS,
			)
		}
		return fmt.Errorf("namespace '%s': %w", spectrumScaleOperatorNS, err)
	}

	_, err = mc.clientset.CoreV1().Namespaces().Get(mc.ctx, fusionAccessNS, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"namespace %q not found — preflight requires it for a fresh migration; use --continue to skip preflight if resuming after removal",
				fusionAccessNS,
			)
		}
		return fmt.Errorf("namespace '%s': %w", fusionAccessNS, err)
	}

	printSuccess("Required namespaces validated")
	return nil
}

// --- Webhook operations (cluster-scoped resources) ---

// deleteValidatingWebhookByName deletes a VWC by exact name, falling back to
// searching all VWCs for a webhook entry with the given name.
func deleteValidatingWebhookByName(mc *MigrationContext, name string) error {
	if dryRun {
		printDryRun(fmt.Sprintf("Would delete validatingwebhookconfiguration %s", name))
		return nil
	}

	_, err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(mc.ctx, name, metav1.GetOptions{})
	if err == nil {
		if err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(mc.ctx, name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		printSuccess(fmt.Sprintf("Deleted webhook configuration %s", name))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get webhook %s: %w", name, err)
	}

	vwcList, err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list webhook configurations: %w", err)
	}
	for _, vwc := range vwcList.Items {
		for _, wh := range vwc.Webhooks {
			if wh.Name == name {
				if err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(mc.ctx, vwc.Name, metav1.DeleteOptions{}); err != nil {
					return err
				}
				printSuccess(fmt.Sprintf("Deleted webhook configuration %s (containing %s)", vwc.Name, name))
				return nil
			}
		}
	}

	printSkip(fmt.Sprintf("Webhook %s not found", name))
	return nil
}

func removeValidatingWebhooks(mc *MigrationContext) error {
	if err := deleteValidatingWebhookByName(mc, "vlocaldisk.scale.spectrum.ibm.com"); err != nil {
		return err
	}

	if err := deleteValidatingWebhookByName(mc, "vfilesystem.scale.spectrum.ibm.com"); err != nil {
		return err
	}

	if dryRun {
		printDryRun("Would delete vfilesystemclaim.kb.io-* webhooks")
		return nil
	}

	webhooks, err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list webhook configurations: %w", err)
	}
	for _, wh := range webhooks.Items {
		if strings.HasPrefix(wh.Name, "vfilesystemclaim.kb.io") {
			if err := mc.clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(
				mc.ctx, wh.Name, metav1.DeleteOptions{},
			); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete webhook %s: %w", wh.Name, err)
			}
			printSuccess(fmt.Sprintf("Deleted webhook configuration %s", wh.Name))
		}
	}
	return nil
}

// --- Namespace labeling (namespace is a cluster-scoped resource) ---

func labelUserWorkloadMonitoringNS(mc *MigrationContext) error {
	ns, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, userWorkloadMonitoringNS, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", userWorkloadMonitoringNS, err)
	}
	labels := ns.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels["scale.spectrum.ibm.com/networkpolicy"] == "allow" {
		printSkip(fmt.Sprintf("Namespace %s already labeled", userWorkloadMonitoringNS))
		return nil
	}
	if dryRun {
		printDryRun(fmt.Sprintf("Would label namespace %s with scale.spectrum.ibm.com/networkpolicy=allow", userWorkloadMonitoringNS))
		return nil
	}
	labels["scale.spectrum.ibm.com/networkpolicy"] = "allow"
	ns.SetLabels(labels)
	if _, err := mc.clientset.CoreV1().Namespaces().Update(mc.ctx, ns, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to label namespace %s: %w", userWorkloadMonitoringNS, err)
	}
	printSuccess(fmt.Sprintf("Labeled namespace %s", userWorkloadMonitoringNS))
	return nil
}

// --- StorageClass operations (cluster-scoped resources) ---

// listSpectrumScaleStorageClasses lists StorageClasses that use the IBM Spectrum
// Scale CSI provisioner. Creation from manifests (--storage-class-dir) will be
// added later and will be limited to that provisioner.
func listSpectrumScaleStorageClasses(mc *MigrationContext) error {
	if storageClassDir != "" {
		printSkip("Creating StorageClasses from --storage-class-dir is not implemented yet")
	}

	list, err := mc.clientset.StorageV1().StorageClasses().List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StorageClasses: %w", err)
	}

	var names []string
	for _, sc := range list.Items {
		if sc.Provisioner == spectrumScaleCSIProvisioner {
			names = append(names, sc.Name)
		}
	}

	if len(names) == 0 {
		printSkip(fmt.Sprintf("No StorageClasses with provisioner %s", spectrumScaleCSIProvisioner))
		return nil
	}

	printInfo(fmt.Sprintf("StorageClasses (provisioner %s):", spectrumScaleCSIProvisioner))
	for _, n := range names {
		printInfo(fmt.Sprintf("  %s", n))
	}
	printSuccess(fmt.Sprintf("Listed %d StorageClass(es)", len(names)))
	return nil
}
