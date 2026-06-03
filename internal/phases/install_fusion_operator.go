package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
)

// InstallFusionOperator ensures Namespace, OperatorGroup, CatalogSource, and Subscription for Fusion Operator are present.
func InstallFusionOperator(mc *kube.Context) error {
	if err := cluster.EnsureFusionOperatorResources(mc); err != nil {
		return fmt.Errorf("ensure Fusion Operator install resources: %w", err)
	}
	return nil
}
