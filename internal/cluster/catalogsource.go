package cluster

import (
	"errors"
	"fmt"
	"reflect"
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

	// specWrittenAt records when this call created or changed the CatalogSource; it stays zero when
	// the spec already matched. waitFDFCatalogSourceReady uses it to reject a READY status that
	// still describes the previous index pod.
	var specWrittenAt time.Time

	existing, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
		}
		specWrittenAt = time.Now()
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
		currentSpec, _, cerr := unstructured.NestedMap(existing.Object, "spec")
		if cerr != nil {
			return fmt.Errorf("read spec on CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, cerr)
		}
		if reflect.DeepEqual(currentSpec, spec) {
			output.PrintSkip(fmt.Sprintf(
				"CatalogSource %s in %s already matches the desired spec",
				name, constants.OpenShiftMarketplaceNS,
			))
		} else {
			if err := unstructured.SetNestedMap(existing.Object, spec, "spec"); err != nil {
				return fmt.Errorf("set spec on CatalogSource: %w", err)
			}
			specWrittenAt = time.Now()
			_, err = res.Update(mc.Ctx, existing, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("update CatalogSource %s/%s: %w", constants.OpenShiftMarketplaceNS, name, err)
			}
			output.PrintSuccess(fmt.Sprintf("Updated CatalogSource %s in %s", name, constants.OpenShiftMarketplaceNS))
		}
	}

	return waitFDFCatalogSourceReady(mc, res, name, specWrittenAt)
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

func fdfCatalogReadyDurations(mc *kube.Context) (timeout, interval time.Duration) {
	timeout = constants.FDFCatalogSourceReadyTimeout
	interval = constants.FDFCatalogSourceReadyPollInterval
	if mc != nil && mc.FDFCatalogReadyTimeout > 0 && mc.FDFCatalogReadyPollInterval > 0 {
		timeout = mc.FDFCatalogReadyTimeout
		interval = mc.FDFCatalogReadyPollInterval
	}
	return timeout, interval
}

// catalogSourceReadySince reports whether the CatalogSource is READY, and — when notBefore is
// non-zero — whether that reading describes a connection made at or after notBefore.
//
// The freshness test matters because status.connectionState is owned by catalog-operator and keeps
// describing the *previous* index pod until it notices a new image and rolls it. Immediately after
// the spec is rewritten the status therefore still says READY for the catalog contents we are
// trying to replace, and reading the package list at that point yields the old versions.
//
// If lastConnect is absent or unparseable we cannot verify freshness; rather than block until the
// deadline on an unexpected status shape, the READY is accepted and the caller warns.
func catalogSourceReadySince(u *unstructured.Unstructured, notBefore time.Time) (ready, verified bool) {
	state, _, _ := unstructured.NestedString(u.Object, "status", "connectionState", "lastObservedState")
	if state != catalogSourceConnectionReady {
		return false, false
	}
	if notBefore.IsZero() {
		return true, true
	}

	raw, found, _ := unstructured.NestedString(u.Object, "status", "connectionState", "lastConnect")
	if !found || raw == "" {
		return true, false
	}
	lastConnect, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true, false
	}
	return !lastConnect.Before(notBefore), true
}

// waitFDFCatalogSourceReady blocks until the CatalogSource reports a READY connection state.
//
// specWrittenAt must be the time just before a create or update, or the zero time when the spec was
// left untouched. When set, a READY state is only accepted once catalog-operator has reconnected at
// or after that moment — see catalogSourceReadySince.
func waitFDFCatalogSourceReady(mc *kube.Context, res dynamic.ResourceInterface, name string, specWrittenAt time.Time) error {
	timeout, interval := fdfCatalogReadyDurations(mc)
	deadline := time.Now().Add(timeout)
	warnedUnverified := false

	accept := func(u *unstructured.Unstructured) bool {
		ready, verified := catalogSourceReadySince(u, specWrittenAt)
		if !ready {
			return false
		}
		if !verified && !warnedUnverified {
			warnedUnverified = true
			output.PrintWarning(fmt.Sprintf(
				"CatalogSource %s/%s reports READY but no usable status.connectionState.lastConnect, "+
					"so it cannot be confirmed that this reflects the new catalog image",
				constants.OpenShiftMarketplaceNS, name,
			))
		}
		output.PrintSuccess(fmt.Sprintf(
			"CatalogSource %s/%s is READY",
			constants.OpenShiftMarketplaceNS, name,
		))
		return true
	}

	// helpers.PollUntil sleeps for one interval before its first evaluation, so an untouched and
	// already-READY catalog would otherwise cost a full poll interval. EnsureFDFCatalogSource runs
	// in both preflight and phase 2, which makes that a visible delay on every migration.
	if specWrittenAt.IsZero() {
		if u, gerr := res.Get(mc.Ctx, name, metav1.GetOptions{}); gerr == nil && accept(u) {
			return nil
		}
	}

	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		u, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get CatalogSource %s/%s while waiting: %w", constants.OpenShiftMarketplaceNS, name, err)
		}

		if accept(u) {
			return true, nil
		}
		state, _, _ := unstructured.NestedString(u.Object, "status", "connectionState", "lastObservedState")
		if state == catalogSourceConnectionReady {
			output.PrintInfo(fmt.Sprintf(
				"CatalogSource %s/%s still reports the previous index connection; waiting for it to pick up the new image… (%s remaining)",
				constants.OpenShiftMarketplaceNS, name,
				time.Until(deadline).Round(time.Second),
			))
			return false, nil
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
	}, timeout, interval,
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
			constants.OpenShiftMarketplaceNS, name, timeout, state, msg,
		)
	}
	return err
}
