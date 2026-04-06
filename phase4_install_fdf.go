package main

import "fmt"

// phase4InstallDataFoundation creates the FDF subscription, waits for the Scale
// components to upgrade (6.0.0.2 → 6.0.1.0), and enables monitoring integration.
// ibm-fusion-access is removed in phase 5 after gpfs-module is deleted.
func phase4InstallDataFoundation(mc *MigrationContext) error {
	if err := createFDFSubscriptionAndWait(mc); err != nil {
		return fmt.Errorf("create FDF subscription: %w", err)
	}
	if err := enableGrafanaBridge(mc); err != nil {
		return fmt.Errorf("enable Grafana Bridge: %w", err)
	}
	if err := labelUserWorkloadMonitoringNS(mc); err != nil {
		return fmt.Errorf("label user workload monitoring namespace: %w", err)
	}
	return nil
}
