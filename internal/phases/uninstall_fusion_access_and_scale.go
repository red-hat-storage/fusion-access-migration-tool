package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/fusionaccess"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscaleoperator"
)

// UninstallFusionAccessAndScale removes the FA subscription/CSV, pauses Scale daemon updates on Cluster CRs,
// scales down the Spectrum Scale operator, patches the Fusion Access KMM Module selector for migration,
// and copies entitlement and secure-boot signing secrets to ibm-spectrum-scale.
func UninstallFusionAccessAndScale(mc *kube.Context) error {
	if err := fusionaccess.RemoveSubscriptionAndCSV(mc); err != nil {
		return fmt.Errorf("remove FA subscription and CSV: %w", err)
	}
	if err := spectrumscale.PauseScaleClusterDaemonUpdates(mc); err != nil {
		return fmt.Errorf("pause Scale cluster daemon updates: %w", err)
	}
	if err := spectrumscaleoperator.ScaleDownSpectrumScaleOperator(mc); err != nil {
		return fmt.Errorf("scale down Spectrum Scale operator: %w", err)
	}
	if err := spectrumscale.PatchFusionAccessKMMModuleSelectorForMigration(mc); err != nil {
		return fmt.Errorf("patch Fusion Access KMM Module selector: %w", err)
	}
	secureBoot, err := fusionaccess.CopySecureBootSigningSecretsIfPresent(mc)
	if err != nil {
		return fmt.Errorf("copy secure boot signing secrets: %w", err)
	}
	mc.SecureBootClusterForKMM = secureBoot
	if err := fusionaccess.CopyIBMEntitlementKeyToSpectrumScale(mc); err != nil {
		return fmt.Errorf("copy IBM entitlement key to Spectrum Scale namespace: %w", err)
	}
	return nil
}
