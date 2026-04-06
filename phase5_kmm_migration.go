package main

import "fmt"

// phase5KMMMigration migrates GPFS kernel module management from the old KMM
// setup to the new one managed by FDF. The finalizer must be removed before
// deleting the module, since the finalizer controller may no longer be running.
// ibm-fusion-access is deleted only after gpfs-module is removed from it.
func phase5KMMMigration(mc *MigrationContext) error {
	if err := printKMMModulesInFusionAccess(mc); err != nil {
		return fmt.Errorf("inspect KMM modules: %w", err)
	}
	if err := removeGPFSModuleFinalizer(mc); err != nil {
		return fmt.Errorf("remove gpfs-module finalizer: %w", err)
	}
	if err := deleteGPFSModule(mc); err != nil {
		return fmt.Errorf("delete gpfs-module: %w", err)
	}
	if err := removeFusionAccessNamespace(mc); err != nil {
		return fmt.Errorf("remove FA namespace: %w", err)
	}
	if err := recreateKMMSubscription(mc); err != nil {
		return fmt.Errorf("recreate KMM subscription: %w", err)
	}
	if err := enableKMMInScaleCluster(mc); err != nil {
		return fmt.Errorf("enable KMM in Scale Cluster: %w", err)
	}
	return nil
}
