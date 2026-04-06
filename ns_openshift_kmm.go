// ns_openshift_kmm.go — operations scoped to the openshift-kmm namespace.
// Ensures the namespace exists, KMM OperatorGroup (cluster-wide / AllNamespaces), and subscription.
package main

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ensureOpenShiftKMMNamespace creates openshift-kmm if it does not exist.
func ensureOpenShiftKMMNamespace(mc *MigrationContext) error {
	_, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, kmmNS, metav1.GetOptions{})
	if err == nil {
		printSkip(fmt.Sprintf("Namespace %s already exists", kmmNS))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %s: %w", kmmNS, err)
	}
	if dryRun {
		printDryRun(fmt.Sprintf("Would create namespace %s", kmmNS))
		return nil
	}
	_, err = mc.clientset.CoreV1().Namespaces().Create(mc.ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: kmmNS},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s: %w", kmmNS, err)
	}
	if apierrors.IsAlreadyExists(err) {
		printSkip(fmt.Sprintf("Namespace %s already exists", kmmNS))
	} else {
		printSuccess(fmt.Sprintf("Created namespace %s", kmmNS))
	}
	return nil
}

// ensureOpenShiftKMMOperatorGroup ensures an OLM OperatorGroup exists in
// openshift-kmm before installing KMM. Kernel Module Management is a
// cluster-scoped (AllNamespaces) operator: the Subscription still lives in
// openshift-kmm, but the OperatorGroup uses an empty spec (no targetNamespaces),
// meaning OLM watches all namespaces — not the same as openshift-storage’s
// single-namespace targetNamespaces list.
func ensureOpenShiftKMMOperatorGroup(mc *MigrationContext) error {
	ogList, err := mc.dynamicClient.Resource(operatorGroupGVR).Namespace(kmmNS).List(
		mc.ctx, metav1.ListOptions{},
	)
	if err != nil {
		if dryRun && apierrors.IsNotFound(err) {
			printDryRun(fmt.Sprintf(
				"Would create OperatorGroup %s in %s (empty spec — cluster-wide / AllNamespaces)",
				kmmOperatorGroupName, kmmNS,
			))
			return nil
		}
		return fmt.Errorf("failed to list OperatorGroups in %s: %w", kmmNS, err)
	}
	if len(ogList.Items) > 0 {
		printSkip(fmt.Sprintf("OperatorGroup already present in %s (%s)", kmmNS, ogList.Items[0].GetName()))
		return nil
	}

	og := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      kmmOperatorGroupName,
				"namespace": kmmNS,
			},
			"spec": map[string]interface{}{},
		},
	}

	if dryRun {
		printDryRun(fmt.Sprintf(
			"Would create OperatorGroup %s in %s (empty spec — cluster-wide / AllNamespaces)",
			kmmOperatorGroupName, kmmNS,
		))
		return nil
	}

	_, err = mc.dynamicClient.Resource(operatorGroupGVR).Namespace(kmmNS).Create(
		mc.ctx, og, metav1.CreateOptions{},
	)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create OperatorGroup %s: %w", kmmOperatorGroupName, err)
	}
	if apierrors.IsAlreadyExists(err) {
		printSkip(fmt.Sprintf("OperatorGroup %s already exists", kmmOperatorGroupName))
	} else {
		printSuccess(fmt.Sprintf("Created OperatorGroup %s (cluster-wide)", kmmOperatorGroupName))
	}
	return nil
}

// recreateKMMSubscription creates a new KMM subscription in the openshift-kmm
// namespace, allowing FDF to manage kernel module builds.
func recreateKMMSubscription(mc *MigrationContext) error {
	if err := ensureOpenShiftKMMNamespace(mc); err != nil {
		return err
	}
	if err := ensureOpenShiftKMMOperatorGroup(mc); err != nil {
		return err
	}

	kmmSubscription := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      "kernel-module-management",
				"namespace": kmmNS,
			},
			"spec": map[string]interface{}{
				"channel":             "stable",
				"name":                "kernel-module-management",
				"source":              "redhat-operators",
				"sourceNamespace":     "openshift-marketplace",
				"installPlanApproval": "Automatic",
			},
		},
	}

	if dryRun {
		printDryRun(fmt.Sprintf("Would create KMM subscription in %s", kmmNS))
		return nil
	}

	_, err := mc.dynamicClient.Resource(subscriptionGVR).Namespace(kmmNS).Create(
		mc.ctx, kmmSubscription, metav1.CreateOptions{},
	)
	if apierrors.IsAlreadyExists(err) {
		printSkip("KMM subscription already exists")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create KMM subscription: %w", err)
	}
	printSuccess("Created KMM subscription")
	return nil
}
