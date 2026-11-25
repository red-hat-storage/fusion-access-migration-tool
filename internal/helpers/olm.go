package helpers

import (
	"context"
	"fmt"
	"strings"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// SubscriptionCurrentCSV returns subscription.status.currentCSV when set.
func SubscriptionCurrentCSV(sub *unstructured.Unstructured) (string, bool) {
	s, found, _ := unstructured.NestedString(sub.Object, "status", "currentCSV")
	if !found || s == "" {
		return "", false
	}
	return s, true
}

// CSVStatusPhase returns csv.status.phase (may be empty).
func CSVStatusPhase(csv *unstructured.Unstructured) string {
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	return phase
}

// CSVSpecProviderName returns csv.spec.provider.name.
func CSVSpecProviderName(csv *unstructured.Unstructured) string {
	name, _, _ := unstructured.NestedString(csv.Object, "spec", "provider", "name")
	return name
}

// CSVSpecVersion returns csv.spec.version.
func CSVSpecVersion(csv *unstructured.Unstructured) string {
	v, _, _ := unstructured.NestedString(csv.Object, "spec", "version")
	return v
}

// GetClusterServiceVersion fetches a ClusterServiceVersion by name in namespace.
func GetClusterServiceVersion(ctx context.Context, dyn dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	return dyn.Resource(constants.CsvGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// SubscriptionInstallPlanApproval returns spec.installPlanApproval from a Subscription (e.g. "Automatic" or "Manual").
func SubscriptionInstallPlanApproval(sub *unstructured.Unstructured) string {
	ipa, _, _ := unstructured.NestedString(sub.Object, "spec", "installPlanApproval")
	return ipa
}

// SubscriptionConditionsSummary renders status.conditions as "Type=Status(Reason: Message)" entries.
// OLM reports resolution problems here — ResolutionFailed and CatalogSourcesUnhealthy in particular —
// which is the most useful diagnostic when a Subscription never produces an InstallPlan.
func SubscriptionConditionsSummary(sub *unstructured.Unstructured) string {
	const none = "(no status.conditions)"
	conditions, found, err := unstructured.NestedSlice(sub.Object, "status", "conditions")
	if err != nil || !found || len(conditions) == 0 {
		return none
	}

	parts := make([]string, 0, len(conditions))
	for _, raw := range conditions {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(m, "type")
		status, _, _ := unstructured.NestedString(m, "status")
		reason, _, _ := unstructured.NestedString(m, "reason")
		message, _, _ := unstructured.NestedString(m, "message")

		entry := fmt.Sprintf("%s=%s", condType, status)
		switch {
		case reason != "" && message != "":
			entry += fmt.Sprintf("(%s: %s)", reason, message)
		case reason != "":
			entry += fmt.Sprintf("(%s)", reason)
		case message != "":
			entry += fmt.Sprintf("(%s)", message)
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return none
	}
	return strings.Join(parts, "; ")
}

// SubscriptionInstallPlanRef returns the name from status.installPlanRef on a Subscription.
func SubscriptionInstallPlanRef(sub *unstructured.Unstructured) (string, bool) {
	name, found, _ := unstructured.NestedString(sub.Object, "status", "installPlanRef", "name")
	return name, found && name != ""
}
