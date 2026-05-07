package spectrumscale

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func DeleteFilesystemClaims(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.FilesystemClaimResource)
	list, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystemclaims: %w", err)
	}

	for _, claim := range list.Items {
		if mc.DryRun {
			output.PrintDryRun(fmt.Sprintf("Would delete filesystemclaim %s", claim.GetName()))
			continue
		}
		if err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).Delete(mc.Ctx, claim.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete filesystemclaim %s: %w", claim.GetName(), err)
		}
		output.PrintSuccess(fmt.Sprintf("Deleted filesystemclaim %s", claim.GetName()))
	}
	return nil
}
