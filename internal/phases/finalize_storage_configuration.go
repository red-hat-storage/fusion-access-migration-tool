package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
)

// FinalizeStorageConfiguration lists Spectrum Scale StorageClasses, ensures san-{fs}/san-{fs}-vm classes per filesystem, and verifies filesystem recovery.
func FinalizeStorageConfiguration(mc *kube.Context) error {
	if err := cluster.ListSpectrumScaleStorageClasses(mc); err != nil {
		return fmt.Errorf("list StorageClasses: %w", err)
	}
	if err := cluster.EnsureSANStorageClassesForScaleFilesystems(mc); err != nil {
		return fmt.Errorf("ensure SAN StorageClasses: %w", err)
	}
	if err := spectrumscale.VerifyFilesystemRecovery(mc); err != nil {
		return fmt.Errorf("verify filesystem recovery: %w", err)
	}
	return nil
}
