package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/fusionaccess"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/openshiftkmm"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
)

// MigrateKMM enables KMM on the Scale cluster, waits for the KMM Module in ibm-fusion-access to report no matching nodes,
// deletes that Module, waits for the KMM Module in ibm-spectrum-scale to report matching nodes equal to the daemon-selector node count,
// moves the KMM operator subscription to openshift-kmm, and removes the Fusion Access namespace.
// Secure boot and IBM entitlement were handled in phase 3 (UninstallFusionAccessAndScale).
func MigrateKMM(mc *kube.Context) error {
	if _, err := spectrumscale.PrintKMMModulesInFusionAccess(mc); err != nil {
		return fmt.Errorf("inspect KMM modules: %w", err)
	}
	if err := spectrumscale.EnableKMMInScaleCluster(mc, mc.SecureBootClusterForKMM); err != nil {
		return fmt.Errorf("enable KMM in Scale Cluster: %w", err)
	}
	if err := spectrumscale.WaitForKMMModuleNodesMatching(mc, constants.FusionAccessNS, 0); err != nil {
		return fmt.Errorf("error waiting for KMM Module in namespace %s: %w", constants.FusionAccessNS, err)
	}
	if err := spectrumscale.DeleteFusionAccessKMMModuleStripFinalizers(mc); err != nil {
		return fmt.Errorf("delete Fusion Access KMM Module: %w", err)
	}
	kmmModuleNodesMatching, err := spectrumscale.ScaleDaemonNodeSelectorNodeCount(mc)
	if err != nil {
		return fmt.Errorf("failed to get Scale Daemon node count: %w", err)
	}
	if err := spectrumscale.WaitForKMMModuleNodesMatching(mc, constants.SpectrumScaleNS, kmmModuleNodesMatching); err != nil {
		return fmt.Errorf("error waiting for KMM Module in namespace %s: %w", constants.SpectrumScaleNS, err)
	}
	if err := openshiftkmm.RecreateKMMSubscription(mc); err != nil {
		return fmt.Errorf("recreate KMM subscription: %w", err)
	}
	if err := fusionaccess.RemoveFusionAccessNamespace(mc); err != nil {
		return fmt.Errorf("remove FA namespace: %w", err)
	}
	return nil
}
