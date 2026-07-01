package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/fusionaccess"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/openshiftkmm"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
)

// MigrateKMM enables KMM on the Scale cluster, waits for the Scale Daemon status.versions to confirm
// all nodes upgraded to the target version, asserts the old KMM Module is inactive, deletes it, asserts
// the new KMM Module is fully available, moves the KMM operator subscription to openshift-kmm, and
// removes the Fusion Access namespace.
// Secure boot and IBM entitlement were handled in phase 3 (UninstallFusionAccessAndScale).
func MigrateKMM(mc *kube.Context) error {
	nodeCount, err := spectrumscale.ScaleDaemonNodeSelectorNodeCount(mc)
	if err != nil {
		return fmt.Errorf("get Scale Daemon node count: %w", err)
	}
	if err := spectrumscale.EnableKMMInScaleCluster(mc, mc.SecureBootClusterForKMM); err != nil {
		return fmt.Errorf("enable KMM in Scale Cluster: %w", err)
	}
	if err := spectrumscale.WaitForScaleDaemonVersionUpgrade(mc, nodeCount); err != nil {
		return fmt.Errorf("wait for Scale Daemon version upgrade: %w", err)
	}
	if err := spectrumscale.CheckFusionAccessKMMModuleNodesZero(mc); err != nil {
		return fmt.Errorf("check old KMM Module: %w", err)
	}
	if err := spectrumscale.DeleteFusionAccessKMMModuleStripFinalizers(mc); err != nil {
		return fmt.Errorf("delete Fusion Access KMM Module: %w", err)
	}
	if err := spectrumscale.CheckSpectrumScaleKMMModuleNodesMatching(mc, nodeCount); err != nil {
		return fmt.Errorf("check new KMM Module: %w", err)
	}
	if err := openshiftkmm.RecreateKMMSubscription(mc); err != nil {
		return fmt.Errorf("recreate KMM subscription: %w", err)
	}
	if err := fusionaccess.RemoveFusionAccessNamespace(mc); err != nil {
		return fmt.Errorf("remove FA namespace: %w", err)
	}
	return nil
}
