package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// catalogSourceConnectionReady is the OLM gRPC catalog lastObservedState when the index is usable.
const catalogSourceConnectionReady = "READY"

// EnsureFDFCatalogSource creates or updates the isf-data-foundation-catalog CatalogSource in openshift-marketplace
// and waits until status.connectionState.lastObservedState is READY.
func EnsureFDFCatalogSource(mc *kube.Context) error {
	if mc.DryRun {
		img := mc.FDFCatalogImage
		if img == "" {
			img = "(FDF_CATALOG_IMAGE not set)"
		}
		output.PrintDryRun(fmt.Sprintf(
			"Would create or update CatalogSource %s/%s and wait until READY (image %s)",
			constants.OpenShiftMarketplaceNS, constants.FDFCatalogSourceName, img,
		))
		return nil
	}

	if mc.FDFCatalogImage == "" {
		return fmt.Errorf("FDF_CATALOG_IMAGE is required to install the FDF catalog")
	}

	desired := fdfCatalogSourceObject(mc.FDFCatalogImage)
	res := mc.Dynamic.Resource(constants.CatalogSourceGVR).Namespace(constants.OpenShiftMarketplaceNS)
	name := constants.FDFCatalogSourceName

	existing, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
		}
		_, err = res.Create(mc.Ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
		}
		output.PrintSuccess(fmt.Sprintf("Created CatalogSource %s in %s", name, constants.OpenShiftMarketplaceNS))
	} else {
		spec, found, nerr := unstructured.NestedMap(desired.Object, "spec")
		if nerr != nil || !found {
			return fmt.Errorf("expected spec on desired CatalogSource: found=%v err=%v", found, nerr)
		}
		if err := unstructured.SetNestedMap(existing.Object, spec, "spec"); err != nil {
			return fmt.Errorf("set spec on CatalogSource: %w", err)
		}
		_, err = res.Update(mc.Ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
		}
		output.PrintSuccess(fmt.Sprintf("Updated CatalogSource %s in %s", name, constants.OpenShiftMarketplaceNS))
	}

	return waitFDFCatalogSourceReady(mc, res, name)
}

func fdfCatalogSourceObject(image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "CatalogSource",
			"metadata": map[string]interface{}{
				"name":      constants.FDFCatalogSourceName,
				"namespace": constants.OpenShiftMarketplaceNS,
			},
			"spec": map[string]interface{}{
				"displayName": "Data Foundation Catalog",
				"grpcPodConfig": map[string]interface{}{
					"affinity": map[string]interface{}{
						"nodeAffinity": map[string]interface{}{
							"preferredDuringSchedulingIgnoredDuringExecution": []interface{}{
								map[string]interface{}{
									"preference": map[string]interface{}{
										"matchExpressions": []interface{}{
											map[string]interface{}{
												"key":      "gpu.isf.ibm.com",
												"operator": "DoesNotExist",
											},
										},
									},
									"weight": int64(50),
								},
							},
						},
					},
					"securityContextConfig": "restricted",
				},
				"icon": map[string]interface{}{
					"base64data": "",
					"mediatype":  "",
				},
				"image":      image,
				"publisher":  "IBM",
				"sourceType": "grpc",
				"updateStrategy": map[string]interface{}{
					"registryPoll": map[string]interface{}{
						"interval": "60m",
					},
				},
			},
		},
	}
}

func waitFDFCatalogSourceReady(mc *kube.Context, res dynamic.ResourceInterface, name string) error {
	deadline := time.Now().Add(constants.FDFCatalogSourceReadyTimeout)
	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		u, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get CatalogSource %s/%s while waiting: %w", constants.OpenShiftMarketplaceNS, name, err)
		}

		state, _, _ := unstructured.NestedString(u.Object, "status", "connectionState", "lastObservedState")
		if state == catalogSourceConnectionReady {
			output.PrintSuccess(fmt.Sprintf(
				"CatalogSource %s/%s is READY",
				constants.OpenShiftMarketplaceNS, name,
			))
			return true, nil
		}

		if state == "" {
			output.PrintInfo(fmt.Sprintf(
				"Waiting for CatalogSource %s/%s to report connection state… (%s remaining)",
				constants.OpenShiftMarketplaceNS, name,
				time.Until(deadline).Round(time.Second),
			))
		} else {
			output.PrintInfo(fmt.Sprintf(
				"CatalogSource %s/%s connection state is %q; retrying… (%s remaining)",
				constants.OpenShiftMarketplaceNS, name, state,
				time.Until(deadline).Round(time.Second),
			))
		}

		return false, nil
	}, constants.FDFCatalogSourceReadyTimeout, constants.FDFCatalogSourceReadyPollInterval,
		fmt.Sprintf("CatalogSource %s/%s READY", constants.OpenShiftMarketplaceNS, name))
	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		u, gerr := res.Get(mc.Ctx, name, metav1.GetOptions{})
		state := ""
		msg := "(could not get CatalogSource)"
		if gerr == nil && u != nil {
			state, _, _ = unstructured.NestedString(u.Object, "status", "connectionState", "lastObservedState")
			msg, _, _ = unstructured.NestedString(u.Object, "status", "message")
			if msg == "" {
				msg = "(no status.message)"
			}
		} else if gerr != nil {
			return fmt.Errorf("get CatalogSource %s/%s after wait timeout: %w", constants.OpenShiftMarketplaceNS, name, gerr)
		}
		return fmt.Errorf(
			"CatalogSource %s/%s not READY within %s (lastObservedState=%q; message=%s)",
			constants.OpenShiftMarketplaceNS, name,
			constants.FDFCatalogSourceReadyTimeout, state, msg,
		)
	}
	return err
}
