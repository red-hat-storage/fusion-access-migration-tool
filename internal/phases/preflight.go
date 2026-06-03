package phases

import (
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/cluster"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
)

// PreflightValidations runs connectivity, OCP version, installation, namespace,
// cluster health, Scale readiness, and secure boot signing secret checks.
func PreflightValidations(mc *kube.Context) error {
	if err := cluster.ValidateClusterConnectivity(mc); err != nil {
		return err
	}
	if err := cluster.ValidateOCPVersion(mc); err != nil {
		return err
	}
	if err := cluster.ValidateExistingInstalls(mc); err != nil {
		return err
	}
	if err := cluster.ValidateRequiredNamespaces(mc); err != nil {
		return err
	}
	if err := cluster.ValidateBasicClusterHealth(mc); err != nil {
		return err
	}
	if err := cluster.ValidateScaleClusterExists(mc); err != nil {
		return err
	}
	if err := cluster.ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
		return err
	}
	if err := cluster.ValidateLocalDisksReadyIfPresent(mc); err != nil {
		return err
	}
	if err := cluster.ValidateSecureBootSigningSecrets(mc); err != nil {
		return err
	}
	return nil
}
