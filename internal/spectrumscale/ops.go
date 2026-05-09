// Package spectrumscale — ibm-spectrum-scale and related Scale cluster / KMM Module steps.
package spectrumscale

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func isSpectrumScaleClusterMutatingWebhookNoEndpoints(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no endpoints available for service") &&
		strings.Contains(s, "ibm-spectrum-scale-webhook-service")
}

func EnableGrafanaBridge(mc *kube.Context) error {
	gvr, err := resolveScaleClusterGVR(mc)
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	res := mc.Dynamic.Resource(gvr)
	clusterList, err := res.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		output.PrintSkip("No Scale clusters found")
		return nil
	}

	for i := range clusterList.Items {
		clusterName := clusterList.Items[i].GetName()
		enabled, found, _ := unstructured.NestedBool(clusterList.Items[i].Object, "spec", "grafanaBridge", "enablePrometheusExporter")
		if found && enabled {
			output.PrintSkip(fmt.Sprintf(
				"spec.grafanaBridge.enablePrometheusExporter already true on clusters.scale.spectrum.ibm.com/%s",
				clusterName,
			))
			continue
		}

		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf(
				"Would set clusters.scale.spectrum.ibm.com/%s spec.grafanaBridge.enablePrometheusExporter: true",
				clusterName,
			))
			continue
		}

		for attempt := 1; attempt <= constants.GrafanaBridgeWebhookNoEndpointsMaxAttempts; attempt++ {
			obj, err := res.Get(mc.Ctx, clusterName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get cluster %s: %w", clusterName, err)
			}
			enabled, found, _ := unstructured.NestedBool(obj.Object, "spec", "grafanaBridge", "enablePrometheusExporter")
			if found && enabled {
				output.PrintSkip(fmt.Sprintf(
					"spec.grafanaBridge.enablePrometheusExporter already true on clusters.scale.spectrum.ibm.com/%s",
					clusterName,
				))
				break
			}

			gb, foundGB, err := unstructured.NestedMap(obj.Object, "spec", "grafanaBridge")
			if err != nil {
				return fmt.Errorf("read spec.grafanaBridge on cluster %s: %w", clusterName, err)
			}
			merged := map[string]interface{}{"enablePrometheusExporter": true}
			if foundGB && gb != nil {
				for k, v := range gb {
					if _, ok := merged[k]; !ok {
						merged[k] = v
					}
				}
			}
			if err := unstructured.SetNestedMap(obj.Object, merged, "spec", "grafanaBridge"); err != nil {
				return fmt.Errorf("set spec.grafanaBridge.enablePrometheusExporter on cluster %s: %w", clusterName, err)
			}
			if _, err := res.Update(mc.Ctx, obj, metav1.UpdateOptions{}); err != nil {
				if !isSpectrumScaleClusterMutatingWebhookNoEndpoints(err) {
					return fmt.Errorf("failed to update cluster %s: %w", clusterName, err)
				}
				if attempt >= constants.GrafanaBridgeWebhookNoEndpointsMaxAttempts {
					return fmt.Errorf(
						"failed to update cluster %s after %d attempts (Spectrum Scale mutating webhook had no endpoints): %w",
						clusterName, attempt, err,
					)
				}
				output.PrintInfo(fmt.Sprintf(
					"Spectrum Scale cluster mutating webhook has no endpoints; waiting %v before retry (%d/%d)",
					constants.GrafanaBridgeWebhookNoEndpointsRetryWait, attempt, constants.GrafanaBridgeWebhookNoEndpointsMaxAttempts,
				))
				select {
				case <-time.After(constants.GrafanaBridgeWebhookNoEndpointsRetryWait):
				case <-mc.Ctx.Done():
					return mc.Ctx.Err()
				}
				continue
			}
			output.PrintSuccess(fmt.Sprintf(
				"Set spec.grafanaBridge.enablePrometheusExporter on clusters.scale.spectrum.ibm.com/%s",
				clusterName,
			))
			break
		}
	}
	return nil
}

func scaleClusterDaemonUpdateMatchesDesired(cluster *unstructured.Unstructured) bool {
	update, found, err := unstructured.NestedMap(cluster.Object, "spec", "daemon", "update")
	if err != nil || !found || update == nil {
		return false
	}
	paused, pFound, pErr := unstructured.NestedBool(update, "paused")
	if pErr != nil || !pFound || !paused {
		return false
	}
	maxUnavailable, mFound, mErr := unstructured.NestedInt64(update, "maxUnavailable")
	if mErr != nil || !mFound {
		return false
	}
	return maxUnavailable == 1
}

// PauseScaleClusterDaemonUpdates sets spec.daemon.update.maxUnavailable=1 and paused=true on Scale Cluster CRs.
func PauseScaleClusterDaemonUpdates(mc *kube.Context) error {
	gvr, err := resolveScaleClusterGVR(mc)
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	res := mc.Dynamic.Resource(gvr)
	clusterList, err := res.List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		output.PrintSkip("No Scale clusters found")
		return nil
	}

	for i := range clusterList.Items {
		clusterName := clusterList.Items[i].GetName()
		if scaleClusterDaemonUpdateMatchesDesired(&clusterList.Items[i]) {
			output.PrintSkip(fmt.Sprintf(
				"spec.daemon.update already set (maxUnavailable=1, paused=true) on clusters.scale.spectrum.ibm.com/%s",
				clusterName,
			))
			continue
		}

		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf(
				"Would set clusters.scale.spectrum.ibm.com/%s spec.daemon.update.maxUnavailable=1 spec.daemon.update.paused=true",
				clusterName,
			))
			continue
		}

		obj, err := res.Get(mc.Ctx, clusterName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get cluster %s: %w", clusterName, err)
		}

		daemonSpec, foundDaemon, err := unstructured.NestedMap(obj.Object, "spec", "daemon")
		if err != nil {
			return fmt.Errorf("read spec.daemon on cluster %s: %w", clusterName, err)
		}
		if !foundDaemon || daemonSpec == nil {
			daemonSpec = map[string]interface{}{}
		} else {
			daemonSpec = maps.Clone(daemonSpec)
		}

		updateSpec, foundUpdate, err := unstructured.NestedMap(daemonSpec, "update")
		if err != nil {
			return fmt.Errorf("read spec.daemon.update on cluster %s: %w", clusterName, err)
		}
		if !foundUpdate || updateSpec == nil {
			updateSpec = map[string]interface{}{}
		} else {
			updateSpec = maps.Clone(updateSpec)
		}
		updateSpec["maxUnavailable"] = int64(1)
		updateSpec["paused"] = true
		daemonSpec["update"] = updateSpec

		if err := unstructured.SetNestedMap(obj.Object, daemonSpec, "spec", "daemon"); err != nil {
			return fmt.Errorf("set spec.daemon.update on cluster %s: %w", clusterName, err)
		}
		if _, err := res.Update(mc.Ctx, obj, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update cluster %s: %w", clusterName, err)
		}
		output.PrintSuccess(fmt.Sprintf(
			"Set spec.daemon.update.maxUnavailable=1 and paused=true on clusters.scale.spectrum.ibm.com/%s",
			clusterName,
		))
	}
	return nil
}

func scaleClusterKMMMatchesDesired(cluster *unstructured.Unstructured, secureBoot bool) bool {
	kmm, hasKMM, _ := unstructured.NestedMap(cluster.Object, "spec", "gpfsModuleManagement", "kmm")
	if !hasKMM {
		return false
	}
	if !secureBoot {
		_, hasMS, _ := unstructured.NestedMap(kmm, "moduleSigning")
		return !hasMS && len(kmm) == 0
	}
	ks, kOK, _ := unstructured.NestedString(kmm, "moduleSigning", "keySecret")
	cs, cOK, _ := unstructured.NestedString(kmm, "moduleSigning", "certSecret")
	return kOK && cOK &&
		ks == constants.SecureBootSigningKeySecret &&
		cs == constants.SecureBootSigningKeyPubSecret
}

func scaleClusterDaemonUpdateUnpaused(cluster *unstructured.Unstructured) bool {
	update, found, err := unstructured.NestedMap(cluster.Object, "spec", "daemon", "update")
	if err != nil || !found || update == nil {
		return false
	}
	paused, pFound, pErr := unstructured.NestedBool(update, "paused")
	return pErr == nil && pFound && !paused
}

// EnableKMMInScaleCluster sets metadata labels and spec.gpfsModuleManagement.kmm on clusters.scale.
// For secure-boot clusters (secureBoot true), kmm includes moduleSigning keySecret and certSecret
// pointing at the secrets copied to ibm-spectrum-scale. It also sets spec.daemon.update.paused=false.
func EnableKMMInScaleCluster(mc *kube.Context, secureBoot bool) error {
	gvr, err := resolveScaleClusterGVR(mc)
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	clusterList, err := mc.Dynamic.Resource(gvr).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		output.PrintSkip("No Scale clusters found")
		return nil
	}

	const (
		clusterLabelInstanceKey = "app.kubernetes.io/instance"
		clusterLabelNameKey     = "app.kubernetes.io/name"
		clusterLabelInstanceVal = "ibm-spectrum-scale"
		clusterLabelNameVal     = "cluster"
	)

	for _, cluster := range clusterList.Items {
		labels := cluster.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labelsOK := labels[clusterLabelInstanceKey] == clusterLabelInstanceVal &&
			labels[clusterLabelNameKey] == clusterLabelNameVal

		daemonUnpaused := scaleClusterDaemonUpdateUnpaused(&cluster)
		if labelsOK && scaleClusterKMMMatchesDesired(&cluster, secureBoot) && daemonUnpaused {
			output.PrintSkip(fmt.Sprintf("KMM spec, metadata labels, and daemon update pause already set on cluster %s", cluster.GetName()))
			continue
		}

		if mc.DryRun {
			var parts []string
			if !labelsOK {
				parts = append(parts, "metadata.labels (app.kubernetes.io/instance, app.kubernetes.io/name)")
			}
			if secureBoot {
				parts = append(parts, fmt.Sprintf(
					"spec.gpfsModuleManagement.kmm.moduleSigning.keySecret=%s certSecret=%s",
					constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret,
				))
			} else {
				parts = append(parts, "spec.gpfsModuleManagement.kmm (empty object)")
			}
			if !daemonUnpaused {
				parts = append(parts, "spec.daemon.update.paused=false")
			}
			output.PrintDryRun(fmt.Sprintf("Would merge on cluster %s: %s", cluster.GetName(), strings.Join(parts, " and ")))
			continue
		}

		if !labelsOK {
			merged := make(map[string]string, len(labels)+2)
			for k, v := range labels {
				merged[k] = v
			}
			merged[clusterLabelInstanceKey] = clusterLabelInstanceVal
			merged[clusterLabelNameKey] = clusterLabelNameVal
			cluster.SetLabels(merged)
		}
		var kmmSpec map[string]interface{}
		if secureBoot {
			kmmSpec = map[string]interface{}{
				"moduleSigning": map[string]interface{}{
					"keySecret":  constants.SecureBootSigningKeySecret,
					"certSecret": constants.SecureBootSigningKeyPubSecret,
				},
			}
		} else {
			kmmSpec = map[string]interface{}{}
		}
		if err := unstructured.SetNestedMap(cluster.Object, kmmSpec, "spec", "gpfsModuleManagement", "kmm"); err != nil {
			return fmt.Errorf("failed to set gpfsModuleManagement.kmm on cluster %s: %w", cluster.GetName(), err)
		}
		daemonSpec, foundDaemon, err := unstructured.NestedMap(cluster.Object, "spec", "daemon")
		if err != nil {
			return fmt.Errorf("read spec.daemon on cluster %s: %w", cluster.GetName(), err)
		}
		if !foundDaemon || daemonSpec == nil {
			daemonSpec = map[string]interface{}{}
		} else {
			daemonSpec = maps.Clone(daemonSpec)
		}
		updateSpec, foundUpdate, err := unstructured.NestedMap(daemonSpec, "update")
		if err != nil {
			return fmt.Errorf("read spec.daemon.update on cluster %s: %w", cluster.GetName(), err)
		}
		if !foundUpdate || updateSpec == nil {
			updateSpec = map[string]interface{}{}
		} else {
			updateSpec = maps.Clone(updateSpec)
		}
		updateSpec["paused"] = false
		daemonSpec["update"] = updateSpec
		if err := unstructured.SetNestedMap(cluster.Object, daemonSpec, "spec", "daemon"); err != nil {
			return fmt.Errorf("set spec.daemon.update.paused on cluster %s: %w", cluster.GetName(), err)
		}
		if _, err := mc.Dynamic.Resource(gvr).Update(mc.Ctx, &cluster, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update cluster %s: %w", cluster.GetName(), err)
		}
		output.PrintSuccess(fmt.Sprintf("Applied KMM spec, labels, and daemon update pause on cluster %s", cluster.GetName()))
	}
	return nil
}

func VerifyFilesystemRecovery(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.FilesystemResource)
	deadline := time.Now().Add(constants.FilesystemRecoveryWaitTimeout)
	err := helpers.PollUntil(
		mc.Ctx,
		func() (bool, error) {
			fsList, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to list filesystems: %w", err)
			}

			if len(fsList.Items) == 0 {
				output.PrintInfo(fmt.Sprintf(
					"Waiting for filesystems to be present in %s... (%s remaining)",
					constants.SpectrumScaleNS,
					time.Until(deadline).Round(time.Second),
				))
				return false, nil
			}

			allHealthy := true
			for _, fs := range fsList.Items {
				name := fs.GetName()
				status, _, _ := unstructured.NestedString(fs.Object, "status", "phase")
				mounted, _, _ := unstructured.NestedBool(fs.Object, "status", "mounted")
				if status == "" {
					status = "Unknown"
				}
				output.PrintInfo(fmt.Sprintf("Filesystem %s: phase=%s, mounted=%v", name, status, mounted))
				if !mounted {
					allHealthy = false
				}
			}
			if allHealthy {
				output.PrintSuccess("All filesystems are mounted and functional")
				return true, nil
			}

			output.PrintInfo(fmt.Sprintf(
				"Waiting for all filesystems to be mounted... (%s remaining)",
				time.Until(deadline).Round(time.Second),
			))
			return false, nil
		},
		constants.FilesystemRecoveryWaitTimeout,
		constants.FilesystemRecoveryPollInterval,
		"all Spectrum Scale filesystems are mounted",
	)
	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		return fmt.Errorf("not all filesystems became healthy within %s", constants.FilesystemRecoveryWaitTimeout)
	}
	return err
}
