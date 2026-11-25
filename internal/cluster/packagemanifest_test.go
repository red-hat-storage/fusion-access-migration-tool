package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func testChannel(name, currentCSV, version string) map[string]interface{} {
	channel := map[string]interface{}{
		"name":       name,
		"currentCSV": currentCSV,
	}
	if version != "" {
		channel["currentCSVDesc"] = map[string]interface{}{"version": version}
	}
	return channel
}

func testPackageManifest(name, catalog string, channels ...map[string]interface{}) *unstructured.Unstructured {
	raw := make([]interface{}, 0, len(channels))
	for _, c := range channels {
		raw = append(raw, c)
	}
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.PackageManifestGVR.GroupVersion().String(),
			"kind":       "PackageManifest",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.OpenShiftMarketplaceNS,
			},
			"status": map[string]interface{}{
				"catalogSource": catalog,
				"channels":      raw,
			},
		},
	}
}

func fdfOdfPackageManifest(version string) *unstructured.Unstructured {
	return testPackageManifest(
		constants.OdfOperatorPackageName,
		constants.FDFCatalogSourceName,
		testChannel(constants.OdfSubscriptionChannel, "odf-operator.v"+version, version),
	)
}

func TestFdfOfferedOdfVersion(t *testing.T) {
	t.Run("reads the version from the FDF catalog channel", func(t *testing.T) {
		mc := newClusterTestContext(nil, fdfOdfPackageManifest("4.21.13"))

		csvName, version, found, err := FdfOfferedOdfVersion(mc)
		if err != nil {
			t.Fatalf("FdfOfferedOdfVersion: %v", err)
		}
		if !found {
			t.Fatal("expected the package to be found")
		}
		if version != "4.21.13" {
			t.Fatalf("version = %q, want 4.21.13", version)
		}
		if csvName != "odf-operator.v4.21.13" {
			t.Fatalf("csvName = %q, want odf-operator.v4.21.13", csvName)
		}
	})

	t.Run("not found when the FDF catalog serves no packages", func(t *testing.T) {
		mc := newClusterTestContext(nil)

		_, _, found, err := FdfOfferedOdfVersion(mc)
		if err != nil {
			t.Fatalf("FdfOfferedOdfVersion: %v", err)
		}
		if found {
			t.Fatal("expected found = false")
		}
	})

	// The real packageserver serves one odf-operator PackageManifest per catalog, all with the same
	// name; the fake tracker enforces unique names per namespace and so cannot model that directly.
	// Both cases below exercise the status.catalogSource filter, which is the only thing keeping
	// another catalog's package from being picked up now that the list is unfiltered server-side.
	t.Run("ignores odf-operator served only by another catalog", func(t *testing.T) {
		mc := newClusterTestContext(nil, testPackageManifest(
			constants.OdfOperatorPackageName,
			"redhat-operators",
			testChannel(constants.OdfSubscriptionChannel, "odf-operator.v4.21.15", "4.21.15"),
		))

		_, _, found, err := FdfOfferedOdfVersion(mc)
		if err != nil {
			t.Fatalf("FdfOfferedOdfVersion: %v", err)
		}
		if found {
			t.Fatal("expected found = false for a package from a different catalog")
		}
	})

	t.Run("ignores a package whose status reports a different catalog", func(t *testing.T) {
		pkg := fdfOdfPackageManifest("4.21.13")
		if err := unstructured.SetNestedField(pkg.Object, "redhat-operators", "status", "catalogSource"); err != nil {
			t.Fatalf("seed status.catalogSource: %v", err)
		}
		mc := newClusterTestContext(nil, pkg)

		_, _, found, err := FdfOfferedOdfVersion(mc)
		if err != nil {
			t.Fatalf("FdfOfferedOdfVersion: %v", err)
		}
		if found {
			t.Fatal("expected status.catalogSource to override a stale catalog label")
		}
	})

	t.Run("ignores other packages in the FDF catalog", func(t *testing.T) {
		mc := newClusterTestContext(nil,
			testPackageManifest(
				"ibm-spectrum-scale-operator",
				constants.FDFCatalogSourceName,
				testChannel(constants.OdfSubscriptionChannel, "ibm-spectrum-scale-operator.v6.0.1", "6.0.1"),
			),
		)

		_, _, found, err := FdfOfferedOdfVersion(mc)
		if err != nil {
			t.Fatalf("FdfOfferedOdfVersion: %v", err)
		}
		if found {
			t.Fatal("expected found = false for a catalog without odf-operator")
		}
	})

	t.Run("errors when the subscribed channel is absent", func(t *testing.T) {
		mc := newClusterTestContext(nil, testPackageManifest(
			constants.OdfOperatorPackageName,
			constants.FDFCatalogSourceName,
			testChannel("stable-4.20", "odf-operator.v4.20.9", "4.20.9"),
		))

		_, _, _, err := FdfOfferedOdfVersion(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), constants.OdfSubscriptionChannel) {
			t.Fatalf("error %q does not name the missing channel", err)
		}
	})

	t.Run("errors when the channel reports no version", func(t *testing.T) {
		mc := newClusterTestContext(nil, testPackageManifest(
			constants.OdfOperatorPackageName,
			constants.FDFCatalogSourceName,
			testChannel(constants.OdfSubscriptionChannel, "odf-operator.v4.21.13", ""),
		))

		_, _, _, err := FdfOfferedOdfVersion(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "currentCSVDesc.version") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("errors when the package reports no channels", func(t *testing.T) {
		mc := newClusterTestContext(nil, testPackageManifest(
			constants.OdfOperatorPackageName,
			constants.FDFCatalogSourceName,
		))

		_, _, _, err := FdfOfferedOdfVersion(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no channels") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func odfSubscription(currentCSV string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.SubscriptionGVR.GroupVersion().String(),
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      constants.OdfOperatorSubPrefix,
				"namespace": constants.OpenShiftStorageNS,
			},
			"spec": map[string]interface{}{
				"name": constants.OdfOperatorSubPrefix,
			},
			"status": map[string]interface{}{
				"currentCSV": currentCSV,
			},
		},
	}
}

func odfCSV(name, provider, version string) *unstructured.Unstructured {
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

const testFDFCatalogImage = "icr.io/cpopen/isf-data-foundation-catalog:test"

// readyFdfCatalogSource returns the CatalogSource exactly as EnsureFDFCatalogSource would write it
// for the given image, already reporting READY. Building it from fdfCatalogSourceObject keeps the
// spec byte-identical to the desired one so the ensure step performs no write.
func readyFdfCatalogSource(image string) *unstructured.Unstructured {
	cs := fdfCatalogSourceObject(image)
	_ = unstructured.SetNestedField(
		cs.Object, catalogSourceConnectionReady,
		"status", "connectionState", "lastObservedState",
	)
	return cs
}

// newOdfValidationContext wires a context for the catalog-version validator: openshift-storage
// exists, FDF_CATALOG_IMAGE is set, and the waits are short so a test that is supposed to block
// fails fast instead of sleeping for minutes.
func newOdfValidationContext(dynObjs ...*unstructured.Unstructured) *kube.Context {
	mc := newClusterTestContext(
		kubefake.NewSimpleClientset(coreNamespace(constants.OpenShiftStorageNS)),
		dynObjs...,
	)
	mc.FDFCatalogImage = testFDFCatalogImage
	mc.FDFCatalogReadyTimeout = 150 * time.Millisecond
	mc.FDFCatalogReadyPollInterval = 25 * time.Millisecond
	mc.FDFPackageManifestWaitTimeout = 150 * time.Millisecond
	mc.FDFPackageManifestWaitPollInterval = 25 * time.Millisecond
	return mc
}

func TestValidateFdfCatalogVersionSupportsInstalledOdf(t *testing.T) {
	t.Run("skips when openshift-storage does not exist", func(t *testing.T) {
		mc := newClusterTestContext(nil)
		if err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("skips when there is no odf-operator subscription", func(t *testing.T) {
		mc := newOdfValidationContext()
		if err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("skips when the installed ODF is already IBM", func(t *testing.T) {
		mc := newOdfValidationContext(
			odfSubscription("odf-operator.v4.20.1"),
			odfCSV("odf-operator.v4.20.1", constants.OdfProviderIBM, "4.20.1"),
		)

		if err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc); err != nil {
			t.Fatalf("expected success, got %v", err)
		}

		// The check must short-circuit before paying for the CatalogSource create and READY wait.
		_, err := mc.Dynamic.Resource(constants.CatalogSourceGVR).
			Namespace(constants.OpenShiftMarketplaceNS).
			Get(mc.Ctx, constants.FDFCatalogSourceName, metav1.GetOptions{})
		if err == nil {
			t.Fatal("expected no CatalogSource to be created when the check is unnecessary")
		}
	})

	t.Run("warns but passes in dry-run", func(t *testing.T) {
		mc := newOdfValidationContext(
			odfSubscription("odf-operator.v4.21.15"),
			odfCSV("odf-operator.v4.21.15", constants.OdfProviderRedHat, "4.21.15"),
		)
		mc.DryRun = true

		if err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc); err != nil {
			t.Fatalf("expected dry-run to pass, got %v", err)
		}
	})

	t.Run("blocks when the FDF catalog is older than the installed ODF", func(t *testing.T) {
		mc := newOdfValidationContext(
			odfSubscription("odf-operator.v4.21.15"),
			odfCSV("odf-operator.v4.21.15", constants.OdfProviderRedHat, "4.21.15"),
			readyFdfCatalogSource(testFDFCatalogImage),
			fdfOdfPackageManifest("4.21.13"),
		)

		err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		for _, want := range []string{"4.21.15", "4.21.13", "odf-operator.v4.21.15"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("passes when the FDF catalog is newer than the installed ODF", func(t *testing.T) {
		mc := newOdfValidationContext(
			odfSubscription("odf-operator.v4.21.13"),
			odfCSV("odf-operator.v4.21.13", constants.OdfProviderRedHat, "4.21.13"),
			readyFdfCatalogSource(testFDFCatalogImage),
			fdfOdfPackageManifest("4.21.16"),
		)

		if err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("errors when the FDF catalog serves no odf-operator", func(t *testing.T) {
		mc := newOdfValidationContext(
			odfSubscription("odf-operator.v4.21.15"),
			odfCSV("odf-operator.v4.21.15", constants.OdfProviderRedHat, "4.21.15"),
			readyFdfCatalogSource(testFDFCatalogImage),
		)

		err := ValidateFdfCatalogVersionSupportsInstalledOdf(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "did not publish") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
