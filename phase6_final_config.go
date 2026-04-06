package main

import "fmt"

// phase6FinalConfiguration waits until mmgetstate -a reports every GPFS node active
// (remediating by deleting stale daemon pods when needed), lists Spectrum Scale CSI
// StorageClasses, and verifies filesystem recovery.
// Filesystem verification is advisory — it does not fail the migration if
// filesystems are not yet mounted, as recovery may take time.
func phase6FinalConfiguration(mc *MigrationContext) error {
	if err := waitForGPFSAllNodesActive(mc); err != nil {
		return fmt.Errorf("GPFS cluster not ready: %w", err)
	}
	if err := listSpectrumScaleStorageClasses(mc); err != nil {
		return fmt.Errorf("list StorageClasses: %w", err)
	}
	if err := verifyFilesystemRecovery(mc); err != nil {
		return fmt.Errorf("verify filesystem recovery: %w", err)
	}
	return nil
}
