// ns_spectrum_scale.go — mostly ibm-spectrum-scale; KMM Module CRs are in ibm-fusion-access.
// Handles localdisk/filesystem CR cleanup, filesystemclaim lifecycle, Grafana Bridge,
// gpfs-module in ibm-fusion-access, Scale Cluster gpfsModuleManagement.kmm + labels, filesystem verification.
package main

import (
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// resolveScaleClusterGVR returns a working GVR for scale.spectrum.ibm.com Cluster
// (cluster-scoped). Listing under a namespace 404s; newer clusters may serve v1
// instead of v1beta1.
func resolveScaleClusterGVR(mc *MigrationContext) (schema.GroupVersionResource, error) {
	for _, ver := range []string{"v1", "v1beta1"} {
		gvr := schema.GroupVersionResource{
			Group: "scale.spectrum.ibm.com", Version: ver, Resource: "clusters",
		}
		_, err := mc.dynamicClient.Resource(gvr).List(mc.ctx, metav1.ListOptions{Limit: 1})
		if err == nil {
			return gvr, nil
		}
		if apierrors.IsNotFound(err) {
			continue
		}
		return schema.GroupVersionResource{}, err
	}
	return schema.GroupVersionResource{}, fmt.Errorf(
		"scale.spectrum.ibm.com Cluster API not found (cluster-scoped); tried v1 and v1beta1",
	)
}

// --- OwnerReference and label cleanup ---

func formatOwnerRefsForLog(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		ctrl := ""
		if r.Controller != nil && *r.Controller {
			ctrl = ",controller=true"
		}
		parts = append(parts, fmt.Sprintf("%s %s/%s (uid=%s%s)", r.APIVersion, r.Kind, r.Name, r.UID, ctrl))
	}
	return strings.Join(parts, "; ")
}

// removeOwnerRefsAndLabels strips FA-owned ownerReferences and fusion labels
// from localdisk and filesystem CRs so they survive the operator removal.
func removeOwnerRefsAndLabels(mc *MigrationContext) error {
	for _, resource := range []string{localDiskResource, filesystemResource} {
		if err := processResources(mc, resource); err != nil {
			return fmt.Errorf("failed to process %s: %w", resource, err)
		}
	}
	return nil
}

func processResources(mc *MigrationContext, resourceType string) error {
	printInfo(fmt.Sprintf("Processing %s resources...", resourceType))

	gvr := parseGVR(resourceType)
	list, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		printSkip(fmt.Sprintf("No %s resources found in namespace %s", resourceType, spectrumScaleNS))
		return nil
	}

	if len(list.Items) == 0 {
		printSkip(fmt.Sprintf("No %s resources found in namespace %s", resourceType, spectrumScaleNS))
		return nil
	}

	processed := 0
	modifiedRefs := 0
	modifiedLabels := 0
	skipped := 0
	failed := 0

	for _, item := range list.Items {
		processed++
		resourceModified := false

		if len(item.GetOwnerReferences()) > 0 {
			refs := append([]metav1.OwnerReference(nil), item.GetOwnerReferences()...)
			detail := formatOwnerRefsForLog(refs)
			if dryRun {
				printDryRun(fmt.Sprintf(
					"Would remove ownerReferences from %s/%s: %s",
					resourceType, item.GetName(), detail,
				))
				modifiedRefs++
				resourceModified = true
			} else {
				item.SetOwnerReferences([]metav1.OwnerReference{})
				_, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).Update(mc.ctx, &item, metav1.UpdateOptions{})
				if err != nil {
					printError(fmt.Sprintf(
						"Failed to remove ownerReferences from %s/%s (was: %s): %v",
						resourceType, item.GetName(), detail, err,
					))
					failed++
				} else {
					printSuccess(fmt.Sprintf(
						"Removed ownerReferences from %s/%s: %s",
						resourceType, item.GetName(), detail,
					))
					modifiedRefs++
					resourceModified = true
				}
			}
		}

		labels := item.GetLabels()
		hasFusionLabels := false
		if labels != nil {
			if _, ok := labels["fusion.storage.openshift.io/owned-by-fsc-name"]; ok {
				hasFusionLabels = true
			}
			if _, ok := labels["fusion.storage.openshift.io/owned-by-fsc-namespace"]; ok {
				hasFusionLabels = true
			}
		}

		if hasFusionLabels {
			if dryRun {
				printDryRun(fmt.Sprintf("Would remove fusion labels from %s/%s", resourceType, item.GetName()))
				modifiedLabels++
				resourceModified = true
			} else {
				delete(labels, "fusion.storage.openshift.io/owned-by-fsc-name")
				delete(labels, "fusion.storage.openshift.io/owned-by-fsc-namespace")
				item.SetLabels(labels)
				_, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).Update(mc.ctx, &item, metav1.UpdateOptions{})
				if err != nil {
					printError(fmt.Sprintf("Failed to remove fusion labels from %s/%s: %v", resourceType, item.GetName(), err))
					failed++
				} else {
					printSuccess(fmt.Sprintf("Removed fusion labels from %s/%s", resourceType, item.GetName()))
					modifiedLabels++
					resourceModified = true
				}
			}
		}

		if !resourceModified {
			skipped++
		}
	}

	printInfo(fmt.Sprintf("Summary for %s:", resourceType))
	printInfo(fmt.Sprintf("  Total resources: %d", processed))
	if dryRun {
		printInfo(fmt.Sprintf("  Would remove ownerReferences: %s%d%s", colorGreen, modifiedRefs, colorReset))
		printInfo(fmt.Sprintf("  Would remove fusion labels: %s%d%s", colorGreen, modifiedLabels, colorReset))
	} else {
		printInfo(fmt.Sprintf("  Removed ownerReferences: %s%d%s", colorGreen, modifiedRefs, colorReset))
		printInfo(fmt.Sprintf("  Removed fusion labels: %s%d%s", colorGreen, modifiedLabels, colorReset))
	}
	printInfo(fmt.Sprintf("  Skipped (nothing to remove): %s%d%s", colorYellow, skipped, colorReset))
	if failed > 0 {
		printInfo(fmt.Sprintf("  Failed: %s%d%s", colorRed, failed, colorReset))
		return fmt.Errorf("failed to process %d resources", failed)
	}

	return nil
}

// --- FilesystemClaim operations ---

func removeFinalizersFromFilesystemClaims(mc *MigrationContext) error {
	gvr := parseGVR(filesystemClaimResource)
	list, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystemclaims: %w", err)
	}

	for _, claim := range list.Items {
		if len(claim.GetFinalizers()) == 0 {
			continue
		}
		if dryRun {
			printDryRun(fmt.Sprintf("Would remove finalizers from filesystemclaim %s", claim.GetName()))
			continue
		}
		claim.SetFinalizers([]string{})
		if _, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).Update(mc.ctx, &claim, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to remove finalizers from %s: %w", claim.GetName(), err)
		}
		printSuccess(fmt.Sprintf("Removed finalizers from filesystemclaim %s", claim.GetName()))
	}
	return nil
}

func deleteFilesystemClaims(mc *MigrationContext) error {
	gvr := parseGVR(filesystemClaimResource)
	list, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystemclaims: %w", err)
	}

	for _, claim := range list.Items {
		if dryRun {
			printDryRun(fmt.Sprintf("Would delete filesystemclaim %s", claim.GetName()))
			continue
		}
		if err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).Delete(mc.ctx, claim.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete filesystemclaim %s: %w", claim.GetName(), err)
		}
		printSuccess(fmt.Sprintf("Deleted filesystemclaim %s", claim.GetName()))
	}
	return nil
}

// --- Grafana Bridge ---

func enableGrafanaBridge(mc *MigrationContext) error {
	gvr, err := resolveScaleClusterGVR(mc)
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	clusterList, err := mc.dynamicClient.Resource(gvr).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		printSkip("No Scale clusters found")
		return nil
	}

	for _, cluster := range clusterList.Items {
		enabled, found, _ := unstructured.NestedBool(cluster.Object, "spec", "grafanaBridge", "enablePrometheusExporter")
		if found && enabled {
			printSkip(fmt.Sprintf(
				"spec.grafanaBridge.enablePrometheusExporter already true on clusters.scale.spectrum.ibm.com/%s",
				cluster.GetName(),
			))
			continue
		}

		if dryRun {
			printDryRun(fmt.Sprintf(
				"Would set clusters.scale.spectrum.ibm.com/%s spec.grafanaBridge.enablePrometheusExporter: true",
				cluster.GetName(),
			))
			continue
		}

		gb, foundGB, err := unstructured.NestedMap(cluster.Object, "spec", "grafanaBridge")
		if err != nil {
			return fmt.Errorf("read spec.grafanaBridge on cluster %s: %w", cluster.GetName(), err)
		}
		merged := map[string]interface{}{"enablePrometheusExporter": true}
		if foundGB && gb != nil {
			for k, v := range gb {
				if _, ok := merged[k]; !ok {
					merged[k] = v
				}
			}
		}
		if err := unstructured.SetNestedMap(cluster.Object, merged, "spec", "grafanaBridge"); err != nil {
			return fmt.Errorf("set spec.grafanaBridge.enablePrometheusExporter on cluster %s: %w", cluster.GetName(), err)
		}
		if _, err := mc.dynamicClient.Resource(gvr).Update(mc.ctx, &cluster, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update cluster %s: %w", cluster.GetName(), err)
		}
		printSuccess(fmt.Sprintf(
			"Set spec.grafanaBridge.enablePrometheusExporter on clusters.scale.spectrum.ibm.com/%s",
			cluster.GetName(),
		))
	}
	return nil
}

// --- KMM Module operations (Module CRs in ibm-fusion-access) ---

// printKMMModulesInFusionAccess lists all resources for oc get modules.kmm.sigs.x-k8s.io
// in ibm-fusion-access (read-only). Call before finalizer removal / delete so output
// appears in normal and dry-run runs.
func printKMMModulesInFusionAccess(mc *MigrationContext) error {
	list, err := mc.dynamicClient.Resource(kmmModuleGVR).Namespace(fusionAccessNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			printSkip(fmt.Sprintf("Namespace %s not found — skipping %s listing (resume after namespace removed)", fusionAccessNS, kmmModulesResource))
			return nil
		}
		return fmt.Errorf("list %s in %s: %w", kmmModulesResource, fusionAccessNS, err)
	}

	printInfo(fmt.Sprintf("%s in namespace %s (oc get %s -n %s):",
		kmmModulesResource, fusionAccessNS, kmmModulesResource, fusionAccessNS))
	if len(list.Items) == 0 {
		printInfo("  (none)")
		return nil
	}
	for _, m := range list.Items {
		fin := m.GetFinalizers()
		finDesc := "(none)"
		if len(fin) > 0 {
			finDesc = strings.Join(fin, ", ")
		}
		printInfo(fmt.Sprintf(
			"  name=%s  generation=%d  resourceVersion=%s  finalizers=[%s]",
			m.GetName(), m.GetGeneration(), m.GetResourceVersion(), finDesc,
		))
	}
	return nil
}

// removeGPFSModuleFinalizer strips all finalizers from the gpfs-module Module CR
// so it can be cleanly deleted (the finalizer controller may no longer be running).
func removeGPFSModuleFinalizer(mc *MigrationContext) error {
	module, err := mc.dynamicClient.Resource(kmmModuleGVR).Namespace(fusionAccessNS).Get(
		mc.ctx, gpfsModuleName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		printSkip(fmt.Sprintf("%s/%s not found in %s (or namespace missing)", kmmModulesResource, gpfsModuleName, fusionAccessNS))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get %s %s: %w", kmmModulesResource, gpfsModuleName, err)
	}

	if len(module.GetFinalizers()) == 0 {
		printSkip(fmt.Sprintf("%s/%s in %s has no finalizers", kmmModulesResource, gpfsModuleName, fusionAccessNS))
		return nil
	}
	if dryRun {
		printDryRun(fmt.Sprintf(
			"Would remove finalizers from %s %s/%s (current: %s)",
			kmmModulesResource, fusionAccessNS, gpfsModuleName, strings.Join(module.GetFinalizers(), ", "),
		))
		return nil
	}

	module.SetFinalizers([]string{})
	if _, err := mc.dynamicClient.Resource(kmmModuleGVR).Namespace(fusionAccessNS).Update(mc.ctx, module, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to remove finalizer from %s %s: %w", kmmModulesResource, gpfsModuleName, err)
	}
	printSuccess(fmt.Sprintf("Removed finalizers from %s %s in %s", kmmModulesResource, gpfsModuleName, fusionAccessNS))
	return nil
}

func deleteGPFSModule(mc *MigrationContext) error {
	mod, err := mc.dynamicClient.Resource(kmmModuleGVR).Namespace(fusionAccessNS).Get(
		mc.ctx, gpfsModuleName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		printSkip(fmt.Sprintf("%s/%s already deleted from %s", kmmModulesResource, gpfsModuleName, fusionAccessNS))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get %s %s: %w", kmmModulesResource, gpfsModuleName, err)
	}
	if dryRun {
		printDryRun(fmt.Sprintf(
			"Would delete %s %s/%s (generation=%d, resourceVersion=%s)",
			kmmModulesResource, fusionAccessNS, gpfsModuleName, mod.GetGeneration(), mod.GetResourceVersion(),
		))
		return nil
	}
	if err := mc.dynamicClient.Resource(kmmModuleGVR).Namespace(fusionAccessNS).Delete(
		mc.ctx, gpfsModuleName, metav1.DeleteOptions{},
	); err != nil {
		return fmt.Errorf("failed to delete %s %s: %w", kmmModulesResource, gpfsModuleName, err)
	}
	printSuccess(fmt.Sprintf("Deleted %s %s from %s", kmmModulesResource, gpfsModuleName, fusionAccessNS))
	return nil
}

// --- Scale Cluster KMM enablement ---

func enableKMMInScaleCluster(mc *MigrationContext) error {
	gvr, err := resolveScaleClusterGVR(mc)
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	clusterList, err := mc.dynamicClient.Resource(gvr).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		printSkip("No Scale clusters found")
		return nil
	}

	const (
		clusterLabelInstanceKey = "app.kubernetes.io/instance"
		clusterLabelNameKey     = "app.kubernetes.io/name"
		clusterLabelInstanceVal = "ibm-spectrum-scale"
		clusterLabelNameVal     = "cluster"
	)

	for _, cluster := range clusterList.Items {
		_, hasKMM, _ := unstructured.NestedMap(cluster.Object, "spec", "gpfsModuleManagement", "kmm")

		labels := cluster.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labelsOK := labels[clusterLabelInstanceKey] == clusterLabelInstanceVal &&
			labels[clusterLabelNameKey] == clusterLabelNameVal

		if hasKMM && labelsOK {
			printSkip(fmt.Sprintf("KMM spec and metadata labels already set on cluster %s", cluster.GetName()))
			continue
		}

		if dryRun {
			var parts []string
			if !labelsOK {
				parts = append(parts, "metadata.labels (app.kubernetes.io/instance, app.kubernetes.io/name)")
			}
			if !hasKMM {
				parts = append(parts, "spec.gpfsModuleManagement.kmm (empty object)")
			}
			printDryRun(fmt.Sprintf("Would merge on cluster %s: %s", cluster.GetName(), strings.Join(parts, " and ")))
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
		if !hasKMM {
			if err := unstructured.SetNestedMap(
				cluster.Object, map[string]interface{}{}, "spec", "gpfsModuleManagement", "kmm",
			); err != nil {
				return fmt.Errorf("failed to set gpfsModuleManagement.kmm: %w", err)
			}
		}
		if _, err := mc.dynamicClient.Resource(gvr).Update(mc.ctx, &cluster, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update cluster %s: %w", cluster.GetName(), err)
		}
		printSuccess(fmt.Sprintf("Applied KMM spec and labels on cluster %s", cluster.GetName()))
	}
	return nil
}

// --- Filesystem verification ---

func verifyFilesystemRecovery(mc *MigrationContext) error {
	gvr := parseGVR(filesystemResource)
	fsList, err := mc.dynamicClient.Resource(gvr).Namespace(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystems: %w", err)
	}

	if len(fsList.Items) == 0 {
		printWarning("No filesystems found — manual investigation may be required")
		return nil
	}

	allHealthy := true
	for _, fs := range fsList.Items {
		name := fs.GetName()
		status, _, _ := unstructured.NestedString(fs.Object, "status", "phase")
		mounted, _, _ := unstructured.NestedBool(fs.Object, "status", "mounted")
		if status == "" {
			status = "Unknown"
		}
		printInfo(fmt.Sprintf("Filesystem %s: phase=%s, mounted=%v", name, status, mounted))
		if !mounted {
			allHealthy = false
		}
	}

	if !allHealthy {
		printWarning("Not all filesystems are mounted — verify manually")
	} else {
		printSuccess("All filesystems are mounted and functional")
	}
	return nil
}
