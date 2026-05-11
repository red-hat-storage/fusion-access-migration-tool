package cluster

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnsureFusionOperatorResources creates Namespace, OperatorGroup, CatalogSource, and Subscription for Fusion Operator
// if they do not already exist; existing resources are skipped with a log message.
func EnsureFusionOperatorResources(mc *kube.Context) error {
	if err := EnsureNamespace(mc, constants.FusionOperatorNS); err != nil {
		return err
	}
	if err := ensureFusionOperatorGroup(mc); err != nil {
		return err
	}
	if err := ensureFusionOperatorCatalogSource(mc); err != nil {
		return err
	}
	if err := ensureFusionOperatorSubscription(mc); err != nil {
		return err
	}
	return nil
}

func ensureFusionOperatorGroup(mc *kube.Context) error {
	ogRes := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.FusionOperatorNS)
	_, err := ogRes.Get(mc.Ctx, constants.FusionOperatorGroupName, metav1.GetOptions{})
	if err == nil {
		output.PrintSkip(fmt.Sprintf(
			"OperatorGroup %s already exists in %s",
			constants.FusionOperatorGroupName, constants.FusionOperatorNS,
		))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get OperatorGroup %s in %s: %w", constants.FusionOperatorGroupName, constants.FusionOperatorNS, err)
	}

	og := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      constants.FusionOperatorGroupName,
				"namespace": constants.FusionOperatorNS,
			},
			"spec": map[string]interface{}{
				"targetNamespaces": []interface{}{constants.FusionOperatorNS},
				"upgradeStrategy":  "Default",
			},
		},
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would create OperatorGroup %s in %s (upgradeStrategy=Default)",
			constants.FusionOperatorGroupName, constants.FusionOperatorNS,
		))
		return nil
	}

	_, err = ogRes.Create(mc.Ctx, og, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create OperatorGroup %s: %w", constants.FusionOperatorGroupName, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("OperatorGroup %s already exists", constants.FusionOperatorGroupName))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created OperatorGroup %s", constants.FusionOperatorGroupName))
	}
	return nil
}

func fusionOperatorCatalogSourceObject(image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "CatalogSource",
			"metadata": map[string]interface{}{
				"name":      constants.FusionOperatorCatalogSourceName,
				"namespace": constants.OpenShiftMarketplaceNS,
			},
			"spec": map[string]interface{}{
				"displayName": "IBM Operator Catalog",
				"image":       image,
				"publisher":   "IBM",
				"sourceType":  "grpc",
				"updateStrategy": map[string]interface{}{
					"registryPoll": map[string]interface{}{
						"interval": "45m",
					},
				},
			},
		},
	}
}

func ensureFusionOperatorCatalogSource(mc *kube.Context) error {
	img := mc.FusionOperatorCatalogImage
	if mc.DryRun {
		if img == "" {
			img = "(FUSION_OPERATOR_CATALOG_IMAGE not set)"
		}
		output.PrintDryRun(fmt.Sprintf(
			"Would create CatalogSource %s/%s if missing (image %s)",
			constants.OpenShiftMarketplaceNS, constants.FusionOperatorCatalogSourceName, img,
		))
		return nil
	}
	if img == "" {
		return fmt.Errorf("FUSION_OPERATOR_CATALOG_IMAGE is required to install the Fusion Operator catalog")
	}

	res := mc.Dynamic.Resource(constants.CatalogSourceGVR).Namespace(constants.OpenShiftMarketplaceNS)
	name := constants.FusionOperatorCatalogSourceName

	_, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
	if err == nil {
		output.PrintSkip(fmt.Sprintf(
			"CatalogSource %s already exists in %s",
			name, constants.OpenShiftMarketplaceNS,
		))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
	}

	desired := fusionOperatorCatalogSourceObject(img)
	_, err = res.Create(mc.Ctx, desired, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("CatalogSource %s already exists in %s", name, constants.OpenShiftMarketplaceNS))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created CatalogSource %s in %s", name, constants.OpenShiftMarketplaceNS))
	}
	return nil
}

func fusionOperatorSubscriptionObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      constants.FusionOperatorSubscriptionName,
				"namespace": constants.FusionOperatorNS,
			},
			"spec": map[string]interface{}{
				"channel":             constants.FusionOperatorSubscriptionChannel,
				"name":                constants.FusionOperatorSubscriptionName,
				"source":              constants.FusionOperatorCatalogSourceName,
				"sourceNamespace":     constants.OpenShiftMarketplaceNS,
				"installPlanApproval": "Automatic",
			},
		},
	}
}

func ensureFusionOperatorSubscription(mc *kube.Context) error {
	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionOperatorNS)
	name := constants.FusionOperatorSubscriptionName

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Would create Subscription %s/%s if missing (channel=%s source=%s)",
			constants.FusionOperatorNS, name,
			constants.FusionOperatorSubscriptionChannel, constants.FusionOperatorCatalogSourceName,
		))
		return nil
	}

	_, err := subRes.Get(mc.Ctx, name, metav1.GetOptions{})
	if err == nil {
		output.PrintSkip(fmt.Sprintf("Subscription %s already exists in %s", name, constants.FusionOperatorNS))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get Subscription %s/%s: %w", constants.FusionOperatorNS, name, err)
	}

	desired := fusionOperatorSubscriptionObject()
	_, err = subRes.Create(mc.Ctx, desired, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create Subscription %s/%s: %w", constants.FusionOperatorNS, name, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("Subscription %s already exists in %s", name, constants.FusionOperatorNS))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created Subscription %s in %s", name, constants.FusionOperatorNS))
	}
	return nil
}
