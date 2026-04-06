package cluster

import (
	"fmt"
	"strings"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func deleteValidatingWebhookByName(mc *kube.Context, name string) error {
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would delete validatingwebhookconfiguration %s", name))
		return nil
	}

	_, err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(mc.Ctx, name, metav1.GetOptions{})
	if err == nil {
		if err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(mc.Ctx, name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		output.PrintSuccess(fmt.Sprintf("Deleted webhook configuration %s", name))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get webhook %s: %w", name, err)
	}

	vwcList, err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list webhook configurations: %w", err)
	}
	for _, vwc := range vwcList.Items {
		for _, wh := range vwc.Webhooks {
			if wh.Name == name {
				if err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(mc.Ctx, vwc.Name, metav1.DeleteOptions{}); err != nil {
					return err
				}
				output.PrintSuccess(fmt.Sprintf("Deleted webhook configuration %s (containing %s)", vwc.Name, name))
				return nil
			}
		}
	}

	output.PrintSkip(fmt.Sprintf("Webhook %s not found", name))
	return nil
}

func RemoveValidatingWebhooks(mc *kube.Context) error {
	if err := deleteValidatingWebhookByName(mc, "vlocaldisk.scale.spectrum.ibm.com"); err != nil {
		return err
	}

	if err := deleteValidatingWebhookByName(mc, "vfilesystem.scale.spectrum.ibm.com"); err != nil {
		return err
	}

	if mc.DryRun {
		output.PrintDryRun("Would delete vfilesystemclaim.kb.io-* webhooks")
		return nil
	}

	webhooks, err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list webhook configurations: %w", err)
	}
	for _, wh := range webhooks.Items {
		if strings.HasPrefix(wh.Name, "vfilesystemclaim.kb.io") {
			if err := mc.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(
				mc.Ctx, wh.Name, metav1.DeleteOptions{},
			); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete webhook %s: %w", wh.Name, err)
			}
			output.PrintSuccess(fmt.Sprintf("Deleted webhook configuration %s", wh.Name))
		}
	}
	return nil
}
