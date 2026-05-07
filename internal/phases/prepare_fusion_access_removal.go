package phases

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/fusionaccess"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/spectrumscale"
)

// PrepareFusionAccessRemoval scales down the FA operator, removes webhooks, cleans owner refs / labels / finalizers, and deletes filesystem claims.
func PrepareFusionAccessRemoval(mc *kube.Context) error {
	if err := cluster.EnsureFDFCatalogSource(mc); err != nil {
		return fmt.Errorf("ensure FDF CatalogSource: %w", err)
	}
	if err := fusionaccess.ScaleDownFAOperatorCSV(mc); err != nil {
		return fmt.Errorf("scale down FA operator CSV: %w", err)
	}
	if err := cluster.RemoveValidatingWebhooks(mc); err != nil {
		return fmt.Errorf("remove validating webhooks: %w", err)
	}
	if err := spectrumscale.RemoveOwnerRefsAndLabels(mc); err != nil {
		return fmt.Errorf("remove ownerReferences and labels: %w", err)
	}
	if err := spectrumscale.RemoveFinalizersFromFilesystemClaims(mc); err != nil {
		return fmt.Errorf("remove finalizers from filesystemclaims: %w", err)
	}
	if err := spectrumscale.DeleteFilesystemClaims(mc); err != nil {
		return fmt.Errorf("delete filesystemclaims: %w", err)
	}
	return nil
}
