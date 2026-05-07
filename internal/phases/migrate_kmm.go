package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/fusionaccess"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/openshiftkmm"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
)

// MigrateKMM enables KMM on the Scale cluster, waits for the Fusion Access KMM Module to report no matching nodes,
// deletes that Module (clearing finalizers if deletion is stuck), moves the KMM operator subscription to openshift-kmm, and removes the Fusion Access namespace.
// Secure boot and IBM entitlement were handled in phase 3 (UninstallFusionAccessAndScale).
func MigrateKMM(mc *kube.Context) error {
	if err := spectrumscale.PrintKMMModulesInFusionAccess(mc); err != nil {
		return fmt.Errorf("inspect KMM modules: %w", err)
	}
	if err := spectrumscale.EnableKMMInScaleCluster(mc, mc.SecureBootClusterForKMM); err != nil {
		return fmt.Errorf("enable KMM in Scale Cluster: %w", err)
	}
	if err := spectrumscale.WaitForFusionAccessKMMModuleLoaderNodesMatchingZero(mc); err != nil {
		return fmt.Errorf("wait for KMM Module nodesMatchingSelectorNumber: %w", err)
	}
	if err := spectrumscale.DeleteFusionAccessSingletonKMMModuleStripFinalizers(mc); err != nil {
		return fmt.Errorf("delete Fusion Access KMM Module: %w", err)
	}
	if err := openshiftkmm.RecreateKMMSubscription(mc); err != nil {
		return fmt.Errorf("recreate KMM subscription: %w", err)
	}
	if err := fusionaccess.RemoveFusionAccessNamespace(mc); err != nil {
		return fmt.Errorf("remove FA namespace: %w", err)
	}
	return nil
}
