// Package fusionaccess — operations scoped to the ibm-fusion-access namespace.
package fusionaccess

import (
	"fmt"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ScaleDownFAOperatorCSV(mc *kube.Context) error {
	if _, nsErr := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping FA operator CSV scale-down", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", constants.FusionAccessNS, nsErr)
	}

	subscription, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionAccessNS).Get(
		mc.Ctx, constants.FusionAccessOperatorName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		output.PrintSkip("FA subscription not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get FA subscription: %w", err)
	}

	csvName, ok := helpers.SubscriptionCurrentCSV(subscription)
	if !ok {
		return fmt.Errorf("no currentCSV found in subscription %s", constants.FusionAccessOperatorName)
	}

	csv, err := mc.Dynamic.Resource(constants.CsvGVR).Namespace(constants.FusionAccessNS).Get(mc.Ctx, csvName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		output.PrintSkip(fmt.Sprintf("CSV %s not found", csvName))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get CSV %s: %w", csvName, err)
	}

	deployments, found, _ := unstructured.NestedSlice(csv.Object, "spec", "install", "spec", "deployments")
	if !found || len(deployments) == 0 {
		output.PrintSkip("No deployments found in CSV")
		return nil
	}

	var deploymentNames []string
	for _, dep := range deployments {
		depMap, ok := dep.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(depMap, "name")
		if name != "" {
			deploymentNames = append(deploymentNames, name)
		}
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would scale down %d deployment(s) in CSV %s", len(deployments), csvName))
		return nil
	}

	for i, dep := range deployments {
		depMap, ok := dep.(map[string]interface{})
		if !ok {
			continue
		}
		if err := unstructured.SetNestedField(depMap, int64(0), "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas in CSV deployment: %w", err)
		}
		deployments[i] = depMap
	}

	if err := unstructured.SetNestedSlice(csv.Object, deployments, "spec", "install", "spec", "deployments"); err != nil {
		return fmt.Errorf("failed to update deployments in CSV: %w", err)
	}
	if _, err := mc.Dynamic.Resource(constants.CsvGVR).Namespace(constants.FusionAccessNS).Update(mc.Ctx, csv, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CSV %s: %w", csvName, err)
	}
	output.PrintSuccess(fmt.Sprintf("Scaled down operator in CSV %s", csvName))

	if len(deploymentNames) > 0 {
		output.PrintInfo(fmt.Sprintf("Waiting up to %s for operator deployment(s) to reach zero ready replicas: %v",
			constants.FAOperatorScaleDownWaitTimeout, deploymentNames))
		if err := waitForDeploymentsScaledDown(mc, constants.FusionAccessNS, deploymentNames,
			constants.FAOperatorScaleDownWaitTimeout, constants.FAOperatorScaleDownPollInterval); err != nil {
			return err
		}
		output.PrintSuccess("Fusion Access operator deployment(s) scaled down")
	}

	return nil
}

func waitForDeploymentsScaledDown(mc *kube.Context, namespace string, names []string, timeout, poll time.Duration) error {
	return helpers.PollUntil(mc.Ctx, func() (bool, error) {
		allDown := true
		for _, name := range names {
			dep, err := mc.Clientset.AppsV1().Deployments(namespace).Get(mc.Ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
			}
			specReplicas := int32(0)
			if dep.Spec.Replicas != nil {
				specReplicas = *dep.Spec.Replicas
			}
			if specReplicas != 0 || dep.Status.ReadyReplicas != 0 || dep.Status.Replicas != 0 {
				allDown = false
				break
			}
		}
		return allDown, nil
	}, timeout, poll, fmt.Sprintf("deployments in %s scaled down", namespace))
}

func RemoveSubscriptionAndCSV(mc *kube.Context) error {
	if _, nsErr := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{}); nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			output.PrintSkip(fmt.Sprintf("Namespace %s not found — skipping FA subscription/CSV removal", constants.FusionAccessNS))
			return nil
		}
		return fmt.Errorf("failed to check namespace %s: %w", constants.FusionAccessNS, nsErr)
	}

	subscription, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionAccessNS).Get(
		mc.Ctx, constants.FusionAccessOperatorName, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		output.PrintSkip("FA subscription already removed")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get FA subscription: %w", err)
	}

	csvName, _ := helpers.SubscriptionCurrentCSV(subscription)

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would delete CSV %s and subscription %s", csvName, constants.FusionAccessOperatorName))
		return nil
	}

	if csvName != "" {
		if err := mc.Dynamic.Resource(constants.CsvGVR).Namespace(constants.FusionAccessNS).Delete(
			mc.Ctx, csvName, metav1.DeleteOptions{},
		); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CSV %s: %w", csvName, err)
		}
		output.PrintSuccess(fmt.Sprintf("Deleted CSV %s", csvName))
	}

	if err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionAccessNS).Delete(
		mc.Ctx, constants.FusionAccessOperatorName, metav1.DeleteOptions{},
	); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete subscription %s: %w", constants.FusionAccessOperatorName, err)
	}
	output.PrintSuccess(fmt.Sprintf("Deleted subscription %s", constants.FusionAccessOperatorName))
	return nil
}

// CopyIBMEntitlementKeyToSpectrumScale copies the ibm-entitlement-key secret from ibm-fusion-access to
// ibm-spectrum-scale so pulls keep working after the Fusion Access namespace is removed.
// When resuming from checkpoint, if ibm-fusion-access is already gone, succeeds when the secret already exists in ibm-spectrum-scale.
func CopyIBMEntitlementKeyToSpectrumScale(mc *kube.Context) error {
	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return entitlementKeyWhenFusionAccessNamespaceGone(mc)
		}
		return fmt.Errorf("check namespace %s: %w", constants.FusionAccessNS, err)
	}
	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleNS, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("namespace %s must exist to receive %s: %w", constants.SpectrumScaleNS, constants.IBMEntitlementKeySecret, err)
	}

	src, err := mc.Clientset.CoreV1().Secrets(constants.FusionAccessNS).Get(mc.Ctx, constants.IBMEntitlementKeySecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("secret %s/%s not found — create it in Fusion Access before this migration step",
			constants.FusionAccessNS, constants.IBMEntitlementKeySecret)
	}
	if err != nil {
		return fmt.Errorf("get secret %s/%s: %w", constants.FusionAccessNS, constants.IBMEntitlementKeySecret, err)
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would copy secret %s from %s to %s",
			constants.IBMEntitlementKeySecret, constants.FusionAccessNS, constants.SpectrumScaleNS))
		return nil
	}

	return reconcileSecretCopy(mc, src, constants.SpectrumScaleNS)
}

// reconcileSecretCopy creates or updates a secret in dstNS to match src (name, type, data).
func reconcileSecretCopy(mc *kube.Context, src *corev1.Secret, dstNS string) error {
	name := src.Name
	dst := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: dstNS},
		Type:       src.Type,
		Data:       src.Data,
	}
	existing, err := mc.Clientset.CoreV1().Secrets(dstNS).Get(mc.Ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := mc.Clientset.CoreV1().Secrets(dstNS).Create(mc.Ctx, dst, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create secret %s/%s: %w", dstNS, name, err)
		}
		output.PrintSuccess(fmt.Sprintf("Created secret %s in namespace %s", name, dstNS))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get secret %s/%s: %w", dstNS, name, err)
	}
	existing.Data = dst.Data
	existing.Type = dst.Type
	if _, err := mc.Clientset.CoreV1().Secrets(dstNS).Update(mc.Ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update secret %s/%s: %w", dstNS, name, err)
	}
	output.PrintSuccess(fmt.Sprintf("Updated secret %s in namespace %s", name, dstNS))
	return nil
}

func entitlementKeyWhenFusionAccessNamespaceGone(mc *kube.Context) error {
	if !mc.ResumingFromCheckpoint {
		return fmt.Errorf(
			"namespace %s not found — cannot copy %s (if Fusion Access was already removed and the key is in %s, resume from checkpoint so this step only verifies the destination secret)",
			constants.FusionAccessNS, constants.IBMEntitlementKeySecret, constants.SpectrumScaleNS,
		)
	}
	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleNS, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("namespace %s must exist to verify %s: %w", constants.SpectrumScaleNS, constants.IBMEntitlementKeySecret, err)
	}
	_, secErr := mc.Clientset.CoreV1().Secrets(constants.SpectrumScaleNS).Get(
		mc.Ctx, constants.IBMEntitlementKeySecret, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(secErr) {
		return fmt.Errorf(
			"namespace %s is gone but secret %s/%s does not exist — create it in %s before continuing",
			constants.FusionAccessNS, constants.IBMEntitlementKeySecret, constants.SpectrumScaleNS, constants.SpectrumScaleNS,
		)
	}
	if secErr != nil {
		return fmt.Errorf("get secret %s/%s: %w", constants.SpectrumScaleNS, constants.IBMEntitlementKeySecret, secErr)
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf(
			"Secret %s already in %s (Fusion Access namespace removed — would skip copy)",
			constants.IBMEntitlementKeySecret, constants.SpectrumScaleNS,
		))
		return nil
	}
	output.PrintSkip(fmt.Sprintf(
		"Secret %s already present in %s (Fusion Access namespace removed — resume)",
		constants.IBMEntitlementKeySecret, constants.SpectrumScaleNS,
	))
	return nil
}

// CopySecureBootSigningSecretsIfPresent copies secureboot-signing-key and secureboot-signing-key-pub from
// ibm-fusion-access to ibm-spectrum-scale when both exist in Fusion Access (secure boot cluster).
// If ibm-fusion-access is already gone, returns true when both secrets already exist in ibm-spectrum-scale (resume).
func CopySecureBootSigningSecretsIfPresent(mc *kube.Context) (isSecureBootCluster bool, err error) {
	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleNS, metav1.GetOptions{}); err != nil {
		return false, fmt.Errorf("namespace %s must exist for secure boot signing secrets: %w", constants.SpectrumScaleNS, err)
	}

	faNSExists := true
	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			faNSExists = false
		} else {
			return false, fmt.Errorf("check namespace %s: %w", constants.FusionAccessNS, err)
		}
	}

	if !faNSExists {
		_, errKey := mc.Clientset.CoreV1().Secrets(constants.SpectrumScaleNS).Get(mc.Ctx, constants.SecureBootSigningKeySecret, metav1.GetOptions{})
		_, errPub := mc.Clientset.CoreV1().Secrets(constants.SpectrumScaleNS).Get(mc.Ctx, constants.SecureBootSigningKeyPubSecret, metav1.GetOptions{})
		if apierrors.IsNotFound(errKey) || apierrors.IsNotFound(errPub) {
			output.PrintSkip("Fusion Access namespace gone and secure boot signing secrets not both present in ibm-spectrum-scale — treating as non-secure-boot cluster for KMM")
			return false, nil
		}
		if errKey != nil {
			return false, fmt.Errorf("get secret %s/%s: %w", constants.SpectrumScaleNS, constants.SecureBootSigningKeySecret, errKey)
		}
		if errPub != nil {
			return false, fmt.Errorf("get secret %s/%s: %w", constants.SpectrumScaleNS, constants.SecureBootSigningKeyPubSecret, errPub)
		}
		output.PrintSuccess("Secure boot cluster: signing secrets already present in ibm-spectrum-scale (Fusion Access namespace removed)")
		return true, nil
	}

	keySrc, errKey := mc.Clientset.CoreV1().Secrets(constants.FusionAccessNS).Get(mc.Ctx, constants.SecureBootSigningKeySecret, metav1.GetOptions{})
	pubSrc, errPub := mc.Clientset.CoreV1().Secrets(constants.FusionAccessNS).Get(mc.Ctx, constants.SecureBootSigningKeyPubSecret, metav1.GetOptions{})
	keyMissing := apierrors.IsNotFound(errKey)
	pubMissing := apierrors.IsNotFound(errPub)
	if keyMissing && pubMissing {
		output.PrintSkip(fmt.Sprintf("Secrets %s and %s not in %s — not a secure boot signing configuration for this step",
			constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret, constants.FusionAccessNS))
		return false, nil
	}
	if keyMissing != pubMissing {
		return false, fmt.Errorf("expected both %s and %s in %s for secure boot; found only one",
			constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret, constants.FusionAccessNS)
	}
	if errKey != nil {
		return false, fmt.Errorf("get secret %s/%s: %w", constants.FusionAccessNS, constants.SecureBootSigningKeySecret, errKey)
	}
	if errPub != nil {
		return false, fmt.Errorf("get secret %s/%s: %w", constants.FusionAccessNS, constants.SecureBootSigningKeyPubSecret, errPub)
	}

	output.PrintSuccess(fmt.Sprintf("Secure boot cluster: %s and %s found in %s — recreating them in %s for Scale KMM module signing",
		constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret,
		constants.FusionAccessNS, constants.SpectrumScaleNS))

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would copy secrets %s and %s from %s to %s",
			constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret,
			constants.FusionAccessNS, constants.SpectrumScaleNS))
		return true, nil
	}

	for _, src := range []*corev1.Secret{keySrc, pubSrc} {
		if err := reconcileSecretCopy(mc, src, constants.SpectrumScaleNS); err != nil {
			return false, err
		}
	}
	return true, nil
}

func RemoveFusionAccessNamespace(mc *kube.Context) error {
	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		output.PrintSkip(fmt.Sprintf("Namespace %s already removed", constants.FusionAccessNS))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check namespace %s: %w", constants.FusionAccessNS, err)
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would delete namespace %s", constants.FusionAccessNS))
		return nil
	}
	if err := mc.Clientset.CoreV1().Namespaces().Delete(mc.Ctx, constants.FusionAccessNS, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", constants.FusionAccessNS, err)
	}
	output.PrintSuccess(fmt.Sprintf("Deleted namespace %s", constants.FusionAccessNS))

	output.PrintInfo(fmt.Sprintf("Waiting up to %s for namespace %s to be removed",
		constants.FusionAccessNamespaceDeleteWaitTimeout, constants.FusionAccessNS))
	if err := waitForNamespaceGone(mc, constants.FusionAccessNS,
		constants.FusionAccessNamespaceDeleteWaitTimeout, constants.FusionAccessNamespaceDeletePollInterval); err != nil {
		return err
	}
	output.PrintSuccess(fmt.Sprintf("Namespace %s is gone", constants.FusionAccessNS))
	return nil
}

func waitForNamespaceGone(mc *kube.Context, name string, timeout, poll time.Duration) error {
	return helpers.PollUntil(mc.Ctx, func() (bool, error) {
		_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("get namespace %s: %w", name, err)
		}
		return false, nil
	}, timeout, poll, fmt.Sprintf("namespace %s deletion", name))
}
