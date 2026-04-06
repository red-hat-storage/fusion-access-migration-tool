package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscaleoperator"
)

// InstallDataFoundation installs Data Foundation via the odf-operator Subscription, then enables Grafana Bridge and labels user workload monitoring.
func InstallDataFoundation(mc *kube.Context) error {
	if err := spectrumscaleoperator.CreateFDFSubscriptionAndWait(mc); err != nil {
		return fmt.Errorf("reconcile FDF subscription: %w", err)
	}
	if err := spectrumscale.EnableGrafanaBridge(mc); err != nil {
		return fmt.Errorf("enable Grafana Bridge: %w", err)
	}
	if err := cluster.LabelUserWorkloadMonitoringNS(mc); err != nil {
		return fmt.Errorf("label user workload monitoring namespace: %w", err)
	}
	return nil
}
