// ns_fusion_access.go — operations scoped to the ibm-fusion-access namespace.
// Handles FA operator CSV scaling, OLM subscription/CSV removal, and namespace deletion.
package main

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// scaleDownFAOperatorCSV patches the CSV's spec.install.spec.deployments replicas
// to 0 rather than scaling the Deployment directly, since OLM would reconcile and
// re-create the deployment otherwise.
func scaleDownFAOperatorCSV(mc *MigrationContext) error {
	if _, nsErr := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, fusionAccessNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			printSkip(fmt.Sprintf("Namespace %s not found — skipping FA operator CSV scale-down", fusionAccessNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", fusionAccessNS, nsErr)
	}

	subscription, err := mc.dynamicClient.Resource(subscriptionGVR).Namespace(fusionAccessNS).Get(
		mc.ctx, fusionAccessOperatorName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		printSkip("FA subscription not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get FA subscription: %w", err)
	}

	csvName, found, _ := unstructured.NestedString(subscription.Object, "status", "currentCSV")
	if !found || csvName == "" {
		return fmt.Errorf("no currentCSV found in subscription %s", fusionAccessOperatorName)
	}

	csv, err := mc.dynamicClient.Resource(csvGVR).Namespace(fusionAccessNS).Get(mc.ctx, csvName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		printSkip(fmt.Sprintf("CSV %s not found", csvName))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get CSV %s: %w", csvName, err)
	}

	deployments, found, _ := unstructured.NestedSlice(csv.Object, "spec", "install", "spec", "deployments")
	if !found || len(deployments) == 0 {
		printSkip("No deployments found in CSV")
		return nil
	}

	if dryRun {
		printDryRun(fmt.Sprintf("Would scale down %d deployment(s) in CSV %s", len(deployments), csvName))
		return nil
	}

	for i, dep := range deployments {
		depMap, ok := dep.(map[string]interface{})
		if !ok {
			continue
		}
		if err := unstructured.SetNestedField(depMap, int64(0), "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas in CSV deployment: %w", err)
		}
		deployments[i] = depMap
	}

	if err := unstructured.SetNestedSlice(csv.Object, deployments, "spec", "install", "spec", "deployments"); err != nil {
		return fmt.Errorf("failed to update deployments in CSV: %w", err)
	}
	if _, err := mc.dynamicClient.Resource(csvGVR).Namespace(fusionAccessNS).Update(mc.ctx, csv, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CSV %s: %w", csvName, err)
	}
	printSuccess(fmt.Sprintf("Scaled down operator in CSV %s", csvName))
	return nil
}

// removeSubscriptionAndCSV deletes the FA operator's OLM subscription and its
// associated ClusterServiceVersion (CSV first, then subscription).
func removeSubscriptionAndCSV(mc *MigrationContext) error {
	if _, nsErr := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, fusionAccessNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			printSkip(fmt.Sprintf("Namespace %s not found — skipping FA subscription/CSV removal", fusionAccessNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", fusionAccessNS, nsErr)
	}

	subscription, err := mc.dynamicClient.Resource(subscriptionGVR).Namespace(fusionAccessNS).Get(
		mc.ctx, fusionAccessOperatorName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		printSkip("FA subscription already removed")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get FA subscription: %w", err)
	}

	csvName, found, _ := unstructured.NestedString(subscription.Object, "status", "currentCSV")

	if dryRun {
		printDryRun(fmt.Sprintf("Would delete CSV %s and subscription %s", csvName, fusionAccessOperatorName))
		return nil
	}

	if found && csvName != "" {
		if err := mc.dynamicClient.Resource(csvGVR).Namespace(fusionAccessNS).Delete(
			mc.ctx, csvName, metav1.DeleteOptions{},
		); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CSV %s: %w", csvName, err)
		}
		printSuccess(fmt.Sprintf("Deleted CSV %s", csvName))
	}

	if err := mc.dynamicClient.Resource(subscriptionGVR).Namespace(fusionAccessNS).Delete(
		mc.ctx, fusionAccessOperatorName, metav1.DeleteOptions{},
	); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete subscription %s: %w", fusionAccessOperatorName, err)
	}
	printSuccess(fmt.Sprintf("Deleted subscription %s", fusionAccessOperatorName))
	return nil
}

// removeFusionAccessNamespace deletes the ibm-fusion-access namespace. Call only
// after gpfs-module (and any other required cleanup) is removed from that namespace.
func removeFusionAccessNamespace(mc *MigrationContext) error {
	_, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, fusionAccessNS, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		printSkip(fmt.Sprintf("Namespace %s already removed", fusionAccessNS))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check namespace %s: %w", fusionAccessNS, err)
	}
	if dryRun {
		printDryRun(fmt.Sprintf("Would delete namespace %s", fusionAccessNS))
		return nil
	}
	if err := mc.clientset.CoreV1().Namespaces().Delete(mc.ctx, fusionAccessNS, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", fusionAccessNS, err)
	}
	printSuccess(fmt.Sprintf("Deleted namespace %s", fusionAccessNS))
	return nil
}
