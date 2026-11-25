package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func withLastConnect(cs *unstructured.Unstructured, at time.Time) *unstructured.Unstructured {
	_ = unstructured.SetNestedField(
		cs.Object, at.UTC().Format(time.RFC3339),
		"status", "connectionState", "lastConnect",
	)
	return cs
}

// The failure this guards against: EnsureFDFCatalogSource rewrites spec.image, but
// status.connectionState still describes the index pod for the *previous* image and keeps saying
// READY. Accepting that reading makes preflight compare against the old catalog's versions — the
// exact comparison the version gate exists to get right.
func TestEnsureFDFCatalogSource_rejectsStaleReadyAfterImageChange(t *testing.T) {
	stale := withLastConnect(
		readyFdfCatalogSource("icr.io/cpopen/isf-data-foundation-catalog:previous"),
		time.Now().Add(-1*time.Hour),
	)
	mc := newOdfValidationContext(stale)

	err := EnsureFDFCatalogSource(mc)
	if err == nil {
		t.Fatal("expected the wait to reject a READY that predates the image change")
	}
	if !strings.Contains(err.Error(), "not READY within") {
		t.Fatalf("unexpected error: %v", err)
	}

	// The spec must still have been updated to the new image, even though the wait then failed.
	got, gerr := mc.Dynamic.Resource(constants.CatalogSourceGVR).
		Namespace(constants.OpenShiftMarketplaceNS).
		Get(mc.Ctx, constants.FDFCatalogSourceName, metav1.GetOptions{})
	if gerr != nil {
		t.Fatalf("get CatalogSource: %v", gerr)
	}
	image, _, _ := unstructured.NestedString(got.Object, "spec", "image")
	if image != testFDFCatalogImage {
		t.Fatalf("spec.image = %q, want %q", image, testFDFCatalogImage)
	}
}

func TestEnsureFDFCatalogSource_acceptsReadyAfterImageChange(t *testing.T) {
	// catalog-operator has reconnected since the write, so this READY describes the new image.
	fresh := withLastConnect(
		readyFdfCatalogSource("icr.io/cpopen/isf-data-foundation-catalog:previous"),
		time.Now().Add(1*time.Hour),
	)
	mc := newOdfValidationContext(fresh)

	if err := EnsureFDFCatalogSource(mc); err != nil {
		t.Fatalf("expected a post-write READY to be accepted, got %v", err)
	}
}

func TestEnsureFDFCatalogSource_fastPathWhenSpecUnchanged(t *testing.T) {
	// Same image, same spec: nothing is written, so the existing READY is trusted immediately and
	// no stale-connection check applies.
	mc := newOdfValidationContext(readyFdfCatalogSource(testFDFCatalogImage))

	if err := EnsureFDFCatalogSource(mc); err != nil {
		t.Fatalf("expected an untouched READY catalog to be accepted, got %v", err)
	}
}

func TestEnsureFDFCatalogSource_acceptsReadyWithoutLastConnect(t *testing.T) {
	// No lastConnect to compare against: freshness cannot be verified, and the wait degrades to
	// accepting READY rather than blocking until the deadline on an unexpected status shape.
	mc := newOdfValidationContext(
		readyFdfCatalogSource("icr.io/cpopen/isf-data-foundation-catalog:previous"),
	)

	if err := EnsureFDFCatalogSource(mc); err != nil {
		t.Fatalf("expected READY without lastConnect to be accepted, got %v", err)
	}
}

func TestCatalogSourceReadySince(t *testing.T) {
	now := time.Now()

	t.Run("not ready", func(t *testing.T) {
		cs := readyFdfCatalogSource(testFDFCatalogImage)
		_ = unstructured.SetNestedField(cs.Object, "CONNECTING", "status", "connectionState", "lastObservedState")
		if ready, _ := catalogSourceReadySince(cs, time.Time{}); ready {
			t.Fatal("CONNECTING must not count as ready")
		}
	})

	t.Run("ready with no freshness constraint", func(t *testing.T) {
		ready, verified := catalogSourceReadySince(readyFdfCatalogSource(testFDFCatalogImage), time.Time{})
		if !ready || !verified {
			t.Fatalf("ready=%v verified=%v, want true/true", ready, verified)
		}
	})

	t.Run("stale connection", func(t *testing.T) {
		cs := withLastConnect(readyFdfCatalogSource(testFDFCatalogImage), now.Add(-time.Minute))
		ready, verified := catalogSourceReadySince(cs, now)
		if ready {
			t.Fatal("a connection older than the write must not be accepted")
		}
		if !verified {
			t.Fatal("lastConnect was present and parseable, so the result is verified")
		}
	})

	t.Run("fresh connection", func(t *testing.T) {
		cs := withLastConnect(readyFdfCatalogSource(testFDFCatalogImage), now.Add(time.Minute))
		if ready, _ := catalogSourceReadySince(cs, now); !ready {
			t.Fatal("a connection after the write must be accepted")
		}
	})

	t.Run("unparseable lastConnect degrades to unverified accept", func(t *testing.T) {
		cs := readyFdfCatalogSource(testFDFCatalogImage)
		_ = unstructured.SetNestedField(cs.Object, "not-a-timestamp", "status", "connectionState", "lastConnect")
		ready, verified := catalogSourceReadySince(cs, now)
		if !ready {
			t.Fatal("want accept when freshness cannot be determined")
		}
		if verified {
			t.Fatal("want verified=false so the caller warns")
		}
	})
}
