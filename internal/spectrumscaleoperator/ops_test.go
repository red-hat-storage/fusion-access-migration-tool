package spectrumscaleoperator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newOpsTestContext(objs ...*unstructured.Unstructured) *kube.Context {
	customListKinds := map[schema.GroupVersionResource]string{
		constants.SubscriptionGVR: "SubscriptionList",
		constants.CsvGVR:          "ClusterServiceVersionList",
	}
	runtimeObjs := make([]runtime.Object, 0, len(objs))
	for _, obj := range objs {
		runtimeObjs = append(runtimeObjs, obj)
	}
	return &kube.Context{
		Ctx: context.Background(),
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
			runtime.NewScheme(),
			customListKinds,
			runtimeObjs...,
		),
		// Short enough to keep the suite fast, long enough that a loaded CI box still gets several
		// evaluations before the deadline. The behaviour under test is which CSVs satisfy the wait,
		// not how long it runs.
		FDFCSVWaitTimeout:      2 * time.Second,
		FDFCSVWaitPollInterval: 20 * time.Millisecond,
	}
}

func odfSubscriptionWithCSV(currentCSV string, conditions ...map[string]interface{}) *unstructured.Unstructured {
	status := map[string]interface{}{"currentCSV": currentCSV}
	if len(conditions) > 0 {
		raw := make([]interface{}, 0, len(conditions))
		for _, c := range conditions {
			raw = append(raw, c)
		}
		status["conditions"] = raw
	}
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.SubscriptionGVR.GroupVersion().String(),
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      constants.OdfOperatorSubPrefix,
				"namespace": constants.OpenShiftStorageNS,
			},
			"spec":   map[string]interface{}{"name": constants.OdfOperatorSubPrefix},
			"status": status,
		},
	}
}

func succeededODFCSV(name, provider, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.CsvGVR.GroupVersion().String(),
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.OpenShiftStorageNS,
			},
			"spec": map[string]interface{}{
				"version":  version,
				"provider": map[string]interface{}{"name": provider},
			},
			"status": map[string]interface{}{"phase": "Succeeded"},
		},
	}
}

// A Succeeded Red Hat CSV is exactly what the cluster looks like when the FDF catalog is not newer
// than the installed ODF: OLM issues no InstallPlan, so status.currentCSV still points at the Red
// Hat CSV. The wait must not accept that as a completed migration.
func TestWaitForFDFSubscriptionCSVSucceeded_rejectsRedHatProvider(t *testing.T) {
	mc := newOpsTestContext(
		odfSubscriptionWithCSV("odf-operator.v4.21.15", map[string]interface{}{
			"type":    "ResolutionFailed",
			"status":  "True",
			"reason":  "ConstraintsNotSatisfiable",
			"message": "constraints not satisfiable",
		}),
		succeededODFCSV("odf-operator.v4.21.15", constants.OdfProviderRedHat, "4.21.15"),
	)

	err := waitForFDFSubscriptionCSVSucceeded(mc, constants.OdfOperatorSubPrefix)
	if err == nil {
		t.Fatal("expected a Succeeded Red Hat CSV to be rejected")
	}
	// version= is asserted explicitly: "4.21.15" alone is already satisfied by the CSV name, so it
	// would not catch spec.version being dropped from the diagnostic.
	for _, want := range []string{
		`currentCSV="odf-operator.v4.21.15"`,
		`version="4.21.15"`,
		`provider="` + constants.OdfProviderRedHat + `"`,
		"ResolutionFailed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestWaitForFDFSubscriptionCSVSucceeded_acceptsIBMProvider(t *testing.T) {
	mc := newOpsTestContext(
		odfSubscriptionWithCSV("odf-operator.v4.21.16"),
		succeededODFCSV("odf-operator.v4.21.16", constants.OdfProviderIBM, "4.21.16"),
	)

	if err := waitForFDFSubscriptionCSVSucceeded(mc, constants.OdfOperatorSubPrefix); err != nil {
		t.Fatalf("expected a Succeeded IBM CSV to satisfy the wait, got %v", err)
	}
}

func TestWaitForFDFSubscriptionCSVSucceeded_reportsUnresolvedSubscription(t *testing.T) {
	mc := newOpsTestContext(odfSubscriptionWithCSV(""))

	err := waitForFDFSubscriptionCSVSucceeded(mc, constants.OdfOperatorSubPrefix)
	if err == nil {
		t.Fatal("expected an unresolved subscription to time out")
	}
	if !strings.Contains(err.Error(), "status.currentCSV not set yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}
