// Package openshiftkmm — removes KMM Subscription from Fusion Access when present, then ensures openshift-kmm.
package openshiftkmm

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ensureOpenShiftKMMOperatorGroup(mc *kube.Context) error {
	ogList, err := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.KmmNS).List(
		mc.Ctx, metav1.ListOptions{},
	)
	if err != nil {
		if mc.DryRun && apierrors.IsNotFound(err) {
			output.PrintDryRun(fmt.Sprintf(
				"Would create OperatorGroup %s in %s (empty spec — cluster-wide / AllNamespaces)",
				constants.KmmOperatorGroupName, constants.KmmNS,
			))
			return nil
		}
		return fmt.Errorf("failed to list OperatorGroups in %s: %w", constants.KmmNS, err)
	}
	if len(ogList.Items) > 0 {
		output.PrintSkip(fmt.Sprintf("OperatorGroup already present in %s (%s)", constants.KmmNS, ogList.Items[0].GetName()))
		return nil
	}

	og := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      constants.KmmOperatorGroupName,
				"namespace": constants.KmmNS,
			},
			"spec": map[string]interface{}{},
		},
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would create OperatorGroup %s in %s (empty spec — cluster-wide / AllNamespaces)",
			constants.KmmOperatorGroupName, constants.KmmNS,
		))
		return nil
	}

	_, err = mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.KmmNS).Create(
		mc.Ctx, og, metav1.CreateOptions{},
	)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create OperatorGroup %s: %w", constants.KmmOperatorGroupName, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("OperatorGroup %s already exists", constants.KmmOperatorGroupName))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created OperatorGroup %s (cluster-wide)", constants.KmmOperatorGroupName))
	}
	return nil
}

// kmmPackageName is the OLM catalog package name (Subscription spec.name). The Subscription metadata.name is
// often different, e.g. kernel-module-management-stable-redhat-operators-openshift-marketplace.
const kmmPackageName = "kernel-module-management"

// kmmSubscriptionResourceName is the metadata.name used when creating the Subscription in openshift-kmm.
const kmmSubscriptionResourceName = "kernel-module-management"

// subscriptionNamesForPackage lists Subscription resource names in ns whose spec.name matches packageName.
func subscriptionNamesForPackage(mc *kube.Context, ns, packageName string) ([]string, error) {
	list, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(ns).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Subscriptions in %s: %w", ns, err)
	}
	var names []string
	for _, item := range list.Items {
		pkg, found, err := unstructured.NestedString(item.Object, "spec", "name")
		if err != nil || !found {
			continue
		}
		if pkg == packageName {
			names = append(names, item.GetName())
		}
	}
	return names, nil
}

// deleteKMMSubscriptionFromFusionAccessNamespace removes the KMM operator Subscription from ibm-fusion-access
// when that namespace still exists (e.g. resume or re-run). It is a no-op if the namespace or subscription is gone.
// Subscriptions are matched by OLM package name (spec.name), not metadata.name.
func deleteKMMSubscriptionFromFusionAccessNamespace(mc *kube.Context) error {
	ns := constants.FusionAccessNS
	_, nsErr := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, ns, metav1.GetOptions{})
	if apierrors.IsNotFound(nsErr) {
		output.PrintSkip(fmt.Sprintf("Namespace %s not found — no KMM subscription to remove there", ns))
		return nil
	}
	if nsErr != nil {
		return fmt.Errorf("get namespace %s: %w", ns, nsErr)
	}

	names, err := subscriptionNamesForPackage(mc, ns, kmmPackageName)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		output.PrintSkip(fmt.Sprintf("No KMM Subscription (package %q) in %s", kmmPackageName, ns))
		return nil
	}

	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(ns)
	csvRes := mc.Dynamic.Resource(constants.CsvGVR).Namespace(ns)

	for _, subName := range names {
		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf("Would delete KMM CSV (if any) then Subscription %q (package %q) from %s", subName, kmmPackageName, ns))
			continue
		}
		sub, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("get Subscription %s/%s: %w", ns, subName, err)
		}
		if err == nil {
			if csvName, ok := helpers.SubscriptionCurrentCSV(sub); ok {
				csvDelErr := csvRes.Delete(mc.Ctx, csvName, metav1.DeleteOptions{})
				if csvDelErr != nil && !apierrors.IsNotFound(csvDelErr) {
					return fmt.Errorf("delete CSV %s/%s: %w", ns, csvName, csvDelErr)
				}
				if apierrors.IsNotFound(csvDelErr) {
					output.PrintSkip(fmt.Sprintf("KMM CSV %q already gone from %s", csvName, ns))
				} else {
					output.PrintSuccess(fmt.Sprintf("Deleted KMM CSV %q from %s", csvName, ns))
				}
			}
		}
		if err := subRes.Delete(mc.Ctx, subName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete Subscription %s/%s: %w", ns, subName, err)
		}
		output.PrintSuccess(fmt.Sprintf("Deleted KMM Subscription %q (package %q) from %s", subName, kmmPackageName, ns))
	}
	return nil
}

func RecreateKMMSubscription(mc *kube.Context) error {
	if err := deleteKMMSubscriptionFromFusionAccessNamespace(mc); err != nil {
		return err
	}
	if err := cluster.EnsureNamespace(mc, constants.KmmNS); err != nil {
		return err
	}
	if err := ensureOpenShiftKMMOperatorGroup(mc); err != nil {
		return err
	}

	existing, err := subscriptionNamesForPackage(mc, constants.KmmNS, kmmPackageName)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		output.PrintSkip(fmt.Sprintf("KMM subscription already present in %s (package %q: %v)", constants.KmmNS, kmmPackageName, existing))
		return nil
	}

	kmmSubscription := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      kmmSubscriptionResourceName,
				"namespace": constants.KmmNS,
			},
			"spec": map[string]interface{}{
				"channel":             "stable",
				"name":                kmmPackageName,
				"source":              "redhat-operators",
				"sourceNamespace":     "openshift-marketplace",
				"installPlanApproval": "Automatic",
			},
		},
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would create KMM subscription in %s", constants.KmmNS))
		return nil
	}

	_, err = mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.KmmNS).Create(
		mc.Ctx, kmmSubscription, metav1.CreateOptions{},
	)
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip("KMM subscription already exists")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create KMM subscription: %w", err)
	}
	output.PrintSuccess("Created KMM subscription")
	return nil
}
