package cluster

import (
	"errors"
	"fmt"
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

// EnsureFusionOperatorResources creates Namespace, OperatorGroup, CatalogSource, and Subscription for Fusion Operator
// if they do not already exist; existing resources are skipped with a log message. It then waits until the isf-operator
// Subscription's CSV reports phase Succeeded (skipped in dry-run).
func EnsureFusionOperatorResources(mc *kube.Context) error {
	if err := ensureFusionOperatorOLMInstall(mc); err != nil {
		return err
	}
	if err := waitForFusionOperatorCSVSucceeded(mc); err != nil {
		return err
	}
	return nil
}

// ensureFusionOperatorOLMInstall ensures namespace, OperatorGroup, CatalogSource, and Subscription for Fusion Operator.
func ensureFusionOperatorOLMInstall(mc *kube.Context) error {
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

func fusionOperatorCSVWaitDurations(mc *kube.Context) (timeout, interval time.Duration) {
	timeout = constants.FusionOperatorCSVWaitTimeout
	interval = constants.FusionOperatorCSVWaitPollInterval
	if mc != nil && mc.FusionOperatorCSVWaitTimeout > 0 && mc.FusionOperatorCSVWaitPollInterval > 0 {
		timeout = mc.FusionOperatorCSVWaitTimeout
		interval = mc.FusionOperatorCSVWaitPollInterval
	}
	return timeout, interval
}

// operatorGroupTargetsNamespace reports whether og installs operators into targetNS per OLM rules:
// spec.targetNamespaces containing targetNS, or unset/empty meaning the OperatorGroup's own namespace.
func operatorGroupTargetsNamespace(og *unstructured.Unstructured, targetNS string) (bool, error) {
	ownNS := og.GetNamespace()
	targets, found, err := unstructured.NestedStringSlice(og.Object, "spec", "targetNamespaces")
	if err != nil {
		return false, fmt.Errorf("read spec.targetNamespaces on OperatorGroup %s/%s: %w", ownNS, og.GetName(), err)
	}
	if !found || len(targets) == 0 {
		return ownNS == targetNS, nil
	}
	for _, t := range targets {
		if t == targetNS {
			return true, nil
		}
	}
	return false, nil
}

func findOperatorGroupCoveringFusionNamespace(mc *kube.Context) (string, bool, error) {
	ogRes := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.FusionOperatorNS)
	list, err := ogRes.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return "", false, fmt.Errorf("list OperatorGroups in %s: %w", constants.FusionOperatorNS, err)
	}
	for i := range list.Items {
		og := &list.Items[i]
		ok, err := operatorGroupTargetsNamespace(og, constants.FusionOperatorNS)
		if err != nil {
			return "", false, err
		}
		if ok {
			return og.GetName(), true, nil
		}
	}
	return "", false, nil
}

// ensureFusionOperatorGroup creates OperatorGroup isf-og only when no OperatorGroup in the fusion namespace
// already targets ibm-spectrum-fusion-ns (name may differ).
func ensureFusionOperatorGroup(mc *kube.Context) error {
	ogRes := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.FusionOperatorNS)

	if name, found, err := findOperatorGroupCoveringFusionNamespace(mc); err != nil {
		return err
	} else if found {
		output.PrintSkip(fmt.Sprintf(
			"OperatorGroup %q in %s already targets namespace %s; skipping create of %s",
			name, constants.FusionOperatorNS, constants.FusionOperatorNS, constants.FusionOperatorGroupName,
		))
		return nil
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

	_, err := ogRes.Create(mc.Ctx, og, metav1.CreateOptions{})
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

func fusionOperatorCSVNameAllowed(csvName string) bool {
	if csvName == constants.FusionOperatorSubscriptionName {
		return true
	}
	return strings.HasPrefix(csvName, constants.FusionOperatorCSVNamePrefix)
}

// waitForFusionOperatorCSVSucceeded waits until the CSV referenced by the isf-operator Subscription reports phase Succeeded.
func waitForFusionOperatorCSVSucceeded(mc *kube.Context) error {
	if mc.DryRun {
		output.PrintDryRun("Would wait for isf-operator Subscription CSV to reach Succeeded phase")
		return nil
	}

	subRes := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionOperatorNS)
	subName := constants.FusionOperatorSubscriptionName
	ns := constants.FusionOperatorNS

	waitTimeout, pollInterval := fusionOperatorCSVWaitDurations(mc)
	deadline := time.Now().Add(waitTimeout)
	output.PrintInfo(fmt.Sprintf(
		"Waiting for isf-operator CSV to reach Succeeded in %s (up to %v)…",
		ns, waitTimeout,
	))

	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		sub, err := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, fmt.Errorf("subscription %s/%s not found while waiting for CSV", ns, subName)
			}
			return false, fmt.Errorf("get Subscription %s/%s: %w", ns, subName, err)
		}

		csvName, ok := helpers.SubscriptionCurrentCSV(sub)
		if !ok {
			output.PrintInfo(fmt.Sprintf(
				"Subscription %s/%s has no status.currentCSV yet… (%s remaining)",
				ns, subName, time.Until(deadline).Round(time.Second),
			))
			return false, nil
		}
		if !fusionOperatorCSVNameAllowed(csvName) {
			return false, fmt.Errorf(
				"subscription %s/%s status.currentCSV=%q does not match expected isf-operator CSV naming",
				ns, subName, csvName,
			)
		}

		csv, err := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, ns, csvName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				output.PrintInfo(fmt.Sprintf(
					"CSV %s not found yet… (%s remaining)",
					csvName, time.Until(deadline).Round(time.Second),
				))
				return false, nil
			}
			return false, fmt.Errorf("get CSV %s/%s: %w", ns, csvName, err)
		}

		phase := helpers.CSVStatusPhase(csv)
		if phase == "Succeeded" {
			output.PrintSuccess(fmt.Sprintf("isf-operator CSV %s is Succeeded (phase=%s)", csvName, phase))
			return true, nil
		}
		output.PrintInfo(fmt.Sprintf(
			"CSV %s phase is %q; retrying… (%s remaining)",
			csvName, phase, time.Until(deadline).Round(time.Second),
		))
		return false, nil
	}, waitTimeout, pollInterval,
		fmt.Sprintf("isf-operator CSV Succeeded in %s", ns))

	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		sub, gerr := subRes.Get(mc.Ctx, subName, metav1.GetOptions{})
		csvHint := "(could not read subscription)"
		if gerr == nil && sub != nil {
			if cn, ok := helpers.SubscriptionCurrentCSV(sub); ok {
				csvHint = fmt.Sprintf("currentCSV=%q", cn)
				if csv, cerr := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, ns, cn); cerr == nil && csv != nil {
					csvHint = fmt.Sprintf("currentCSV=%q phase=%q", cn, helpers.CSVStatusPhase(csv))
				}
			} else {
				csvHint = "status.currentCSV not set yet"
			}
		}
		return fmt.Errorf(
			"isf-operator CSV did not reach Succeeded within %v (%s)",
			waitTimeout, csvHint,
		)
	}
	return err
}
