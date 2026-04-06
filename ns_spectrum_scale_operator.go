// ns_spectrum_scale_operator.go — operations scoped to the ibm-spectrum-scale-operator namespace.
// Handles Spectrum Scale operator scaling, openshift-storage OperatorGroup, and FDF subscription creation.
package main

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// scaleDownSpectrumScaleOperator scales ibm-spectrum-scale-controller-manager
// to 0 replicas in ibm-spectrum-scale-operator.
func scaleDownSpectrumScaleOperator(mc *MigrationContext) error {
	if _, nsErr := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, spectrumScaleOperatorNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			printSkip(fmt.Sprintf("Namespace %s not found — skipping Scale operator deployment scaledown", spectrumScaleOperatorNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", spectrumScaleOperatorNS, nsErr)
	}

	deployment, err := mc.clientset.AppsV1().Deployments(spectrumScaleOperatorNS).Get(
		mc.ctx, spectrumScaleController, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		printSkip(fmt.Sprintf("Deployment %s not found", spectrumScaleController))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", spectrumScaleController, err)
	}
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		printSkip(fmt.Sprintf("%s already scaled to 0", spectrumScaleController))
		return nil
	}
	if dryRun {
		printDryRun(fmt.Sprintf("Would scale down %s to 0 replicas", spectrumScaleController))
		return nil
	}

	replicas := int32(0)
	deployment.Spec.Replicas = &replicas
	if _, err := mc.clientset.AppsV1().Deployments(spectrumScaleOperatorNS).Update(mc.ctx, deployment, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to scale down %s: %w", spectrumScaleController, err)
	}
	printSuccess(fmt.Sprintf("Scaled down %s to 0 replicas", spectrumScaleController))
	return nil
}

// ensureOpenShiftStorageNamespace creates openshift-storage if it does not exist.
func ensureOpenShiftStorageNamespace(mc *MigrationContext) error {
	_, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, openShiftStorageNS, metav1.GetOptions{})
	if err == nil {
		printSkip(fmt.Sprintf("Namespace %s already exists", openShiftStorageNS))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %s: %w", openShiftStorageNS, err)
	}
	if dryRun {
		printDryRun(fmt.Sprintf("Would create namespace %s", openShiftStorageNS))
		return nil
	}
	_, err = mc.clientset.CoreV1().Namespaces().Create(mc.ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: openShiftStorageNS},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s: %w", openShiftStorageNS, err)
	}
	if apierrors.IsAlreadyExists(err) {
		printSkip(fmt.Sprintf("Namespace %s already exists", openShiftStorageNS))
	} else {
		printSuccess(fmt.Sprintf("Created namespace %s", openShiftStorageNS))
	}
	return nil
}

// ensureOpenShiftStorageOperatorGroup ensures an OLM OperatorGroup exists in
// openshift-storage so a Subscription can install. If any OperatorGroup is
// already present in the namespace, none is created (OLM allows only one per
// namespace for this pattern).
func ensureOpenShiftStorageOperatorGroup(mc *MigrationContext) error {
	ogList, err := mc.dynamicClient.Resource(operatorGroupGVR).Namespace(openShiftStorageNS).List(
		mc.ctx, metav1.ListOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to list OperatorGroups in %s: %w", openShiftStorageNS, err)
	}
	if len(ogList.Items) > 0 {
		printSkip(fmt.Sprintf("OperatorGroup already present in %s (%s)", openShiftStorageNS, ogList.Items[0].GetName()))
		return nil
	}

	og := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      openShiftStorageOperatorGroupName,
				"namespace": openShiftStorageNS,
			},
			"spec": map[string]interface{}{
				"targetNamespaces": []interface{}{openShiftStorageNS},
			},
		},
	}

	if dryRun {
		printDryRun(fmt.Sprintf("Would create OperatorGroup %s in %s", openShiftStorageOperatorGroupName, openShiftStorageNS))
		return nil
	}

	_, err = mc.dynamicClient.Resource(operatorGroupGVR).Namespace(openShiftStorageNS).Create(
		mc.ctx, og, metav1.CreateOptions{},
	)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create OperatorGroup %s: %w", openShiftStorageOperatorGroupName, err)
	}
	if apierrors.IsAlreadyExists(err) {
		printSkip(fmt.Sprintf("OperatorGroup %s already exists", openShiftStorageOperatorGroupName))
	} else {
		printSuccess(fmt.Sprintf("Created OperatorGroup %s", openShiftStorageOperatorGroupName))
	}
	return nil
}

// createFDFSubscriptionAndWait creates an OLM Subscription for FDF (using the
// odf-operator package name) in openshift-storage and polls until the associated
// CSV reaches the Succeeded phase with provider "IBM", then until the IBM Spectrum
// Scale operator CSV (name prefix spectrumScaleOperatorCSVNamePrefix) in
// ibm-spectrum-scale reaches Succeeded.
func createFDFSubscriptionAndWait(mc *MigrationContext) error {
	if err := ensureOpenShiftStorageNamespace(mc); err != nil {
		return err
	}
	if err := ensureOpenShiftStorageOperatorGroup(mc); err != nil {
		return err
	}

	sourceName, err := discoverFDFCatalogSourceName(mc)
	if err != nil {
		return fmt.Errorf("discover FDF catalog source: %w", err)
	}
	printInfo(fmt.Sprintf(
		"Creating odf-operator Subscription: channel=%s spec.source=%s spec.sourceNamespace=%s",
		odfSubscriptionChannel, sourceName, openShiftMarketplaceNS,
	))

	subName := odfOperatorSubPrefix

	fdfSubscription := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      subName,
				"namespace": openShiftStorageNS,
			},
			"spec": map[string]interface{}{
				"channel":             odfSubscriptionChannel,
				"name":                odfOperatorSubPrefix,
				"source":              sourceName,
				"sourceNamespace":     openShiftMarketplaceNS,
				"installPlanApproval": "Automatic",
			},
		},
	}

	if dryRun {
		printDryRun(fmt.Sprintf(
			"Would create FDF subscription %s in %s (channel %s, source %s, sourceNamespace %s)",
			subName, openShiftStorageNS, odfSubscriptionChannel, sourceName, openShiftMarketplaceNS,
		))
		printDryRun(fmt.Sprintf(
			"Would wait for FDF odf-operator CSV Succeeded, then CSV with prefix %q in %s Succeeded",
			spectrumScaleOperatorCSVNamePrefix, spectrumScaleNS,
		))
		return nil
	}

	_, err = mc.dynamicClient.Resource(subscriptionGVR).Namespace(openShiftStorageNS).Create(
		mc.ctx, fdfSubscription, metav1.CreateOptions{},
	)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create FDF subscription: %w", err)
	}
	if apierrors.IsAlreadyExists(err) {
		printSkip(fmt.Sprintf("Subscription %s already exists", subName))
	} else {
		printSuccess(fmt.Sprintf("Created FDF subscription %s", subName))
	}

	printInfo("Waiting for FDF CSV to reach Succeeded phase (up to 10 minutes)...")

	timeout := time.After(10 * time.Minute)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for FDF CSV to reach Succeeded phase")
		case <-ticker.C:
			sub, err := mc.dynamicClient.Resource(subscriptionGVR).Namespace(openShiftStorageNS).Get(
				mc.ctx, subName, metav1.GetOptions{},
			)
			if err != nil {
				continue
			}
			csvName, found, _ := unstructured.NestedString(sub.Object, "status", "currentCSV")
			if !found || csvName == "" {
				continue
			}
			csv, err := mc.dynamicClient.Resource(csvGVR).Namespace(openShiftStorageNS).Get(
				mc.ctx, csvName, metav1.GetOptions{},
			)
			if err != nil {
				continue
			}
			phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
			if phase == "Succeeded" {
				provider, _, _ := unstructured.NestedString(csv.Object, "spec", "provider", "name")
				printSuccess(fmt.Sprintf("FDF CSV %s is ready (provider: %s)", csvName, provider))
				return waitForSpectrumScaleOperatorCSVAfterFDF(mc)
			}
			printInfo(fmt.Sprintf("Waiting for FDF CSV %s (current phase: %s)...", csvName, phase))
		}
	}
}

// waitForSpectrumScaleOperatorCSVAfterFDF polls until an IBM Spectrum Scale operator
// CSV (metadata.name has prefix spectrumScaleOperatorCSVNamePrefix) in
// ibm-spectrum-scale reaches Succeeded.
func waitForSpectrumScaleOperatorCSVAfterFDF(mc *MigrationContext) error {
	const wait = 15 * time.Minute
	printInfo(fmt.Sprintf(
		"Waiting for IBM Spectrum Scale operator CSV (prefix %q) in %s to reach Succeeded (up to %v)...",
		spectrumScaleOperatorCSVNamePrefix, spectrumScaleNS, wait,
	))

	timeout := time.After(wait)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf(
				"timed out waiting for CSV with prefix %q in %s to reach Succeeded",
				spectrumScaleOperatorCSVNamePrefix, spectrumScaleNS,
			)
		case <-ticker.C:
			list, err := mc.dynamicClient.Resource(csvGVR).Namespace(spectrumScaleNS).List(
				mc.ctx, metav1.ListOptions{},
			)
			if err != nil {
				continue
			}

			var waitingName, waitingPhase string
			foundAny := false
			for i := range list.Items {
				item := &list.Items[i]
				name := item.GetName()
				if !strings.HasPrefix(name, spectrumScaleOperatorCSVNamePrefix) {
					continue
				}
				phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
				if phase == "Succeeded" {
					printSuccess(fmt.Sprintf(
						"Spectrum Scale operator CSV %s is ready in %s (phase Succeeded)",
						name, spectrumScaleNS,
					))
					return nil
				}
				foundAny = true
				waitingName = name
				waitingPhase = phase
			}

			if !foundAny {
				printInfo(fmt.Sprintf(
					"No CSV with prefix %q in %s yet...",
					spectrumScaleOperatorCSVNamePrefix, spectrumScaleNS,
				))
				continue
			}
			if waitingPhase == "" {
				waitingPhase = "Unknown"
			}
			printInfo(fmt.Sprintf(
				"Waiting for CSV %s in %s (current phase: %s)...",
				waitingName, spectrumScaleNS, waitingPhase,
			))
		}
	}
}
