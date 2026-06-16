package cluster

import (
	"fmt"
	"slices"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnableOdfConsolePlugin adds odf-console to spec.plugins on the cluster Console operator CR.
func EnableOdfConsolePlugin(mc *kube.Context) error {
	console, err := mc.Dynamic.Resource(constants.ConsoleGVR).Get(
		mc.Ctx, constants.ConsoleClusterName, metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get Console %s: %w", constants.ConsoleClusterName, err)
	}

	plugins, _, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if err != nil {
		return fmt.Errorf("failed to read spec.plugins on Console %s: %w", constants.ConsoleClusterName, err)
	}
	if slices.Contains(plugins, constants.OdfConsolePlugin) {
		output.PrintSkip(fmt.Sprintf("Console plugin %q already enabled", constants.OdfConsolePlugin))
		return nil
	}

	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would enable Console plugin %q", constants.OdfConsolePlugin))
		return nil
	}

	plugins = append(plugins, constants.OdfConsolePlugin)
	if err := unstructured.SetNestedStringSlice(console.Object, plugins, "spec", "plugins"); err != nil {
		return fmt.Errorf("failed to set spec.plugins on Console %s: %w", constants.ConsoleClusterName, err)
	}
	if _, err := mc.Dynamic.Resource(constants.ConsoleGVR).Update(mc.Ctx, console, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update Console plugin %s: %w", constants.ConsoleClusterName, err)
	}
	output.PrintSuccess(fmt.Sprintf("Enabled Console plugin %q", constants.OdfConsolePlugin))
	return nil
}
