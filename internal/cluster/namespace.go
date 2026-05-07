package cluster

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnsureNamespace creates the namespace if missing. Respects DryRun and treats AlreadyExists as success.
func EnsureNamespace(mc *kube.Context, name string) error {
	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, name, metav1.GetOptions{})
	if err == nil {
		output.PrintSkip(fmt.Sprintf("Namespace %s already exists", name))
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %s: %w", name, err)
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would create namespace %s", name))
		return nil
	}
	_, err = mc.Clientset.CoreV1().Namespaces().Create(mc.Ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s: %w", name, err)
	}
	if apierrors.IsAlreadyExists(err) {
		output.PrintSkip(fmt.Sprintf("Namespace %s already exists", name))
	} else {
		output.PrintSuccess(fmt.Sprintf("Created namespace %s", name))
	}
	return nil
}

func LabelUserWorkloadMonitoringNS(mc *kube.Context) error {
	ns, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.UserWorkloadMonitoringNS, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", constants.UserWorkloadMonitoringNS, err)
	}
	labels := ns.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels["scale.spectrum.ibm.com/networkpolicy"] == "allow" {
		output.PrintSkip(fmt.Sprintf("Namespace %s already labeled", constants.UserWorkloadMonitoringNS))
		return nil
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would label namespace %s with scale.spectrum.ibm.com/networkpolicy=allow", constants.UserWorkloadMonitoringNS))
		return nil
	}
	labels["scale.spectrum.ibm.com/networkpolicy"] = "allow"
	ns.SetLabels(labels)
	if _, err := mc.Clientset.CoreV1().Namespaces().Update(mc.Ctx, ns, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to label namespace %s: %w", constants.UserWorkloadMonitoringNS, err)
	}
	output.PrintSuccess(fmt.Sprintf("Labeled namespace %s", constants.UserWorkloadMonitoringNS))
	return nil
}
