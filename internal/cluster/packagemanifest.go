package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FdfOfferedOdfVersion reports the odf-operator CSV name and version that the FDF catalog offers on
// the channel the install phase subscribes to (constants.OdfSubscriptionChannel).
//
// It reads the OLM packageserver's PackageManifest rather than pulling and parsing the catalog
// image: OLM already runs the index image as a gRPC pod for the CatalogSource, so the package
// contents are available over the normal API with no registry auth or mirror resolution.
//
// found is false only when the FDF catalog serves no odf-operator package — either because the
// CatalogSource does not exist or because the packageserver has not published it yet. Callers must
// retry on that rather than treating it as terminal. A package that exists but lacks the expected
// channel is an error, since the install phase subscribes to exactly that channel.
func FdfOfferedOdfVersion(mc *kube.Context) (csvName, csvVersion string, found bool, err error) {
	// Listed unfiltered and matched in Go on status.catalogSource. The packageserver also sets a
	// "catalog" label that could filter server-side, but relying on it would make an unverified
	// label name load-bearing: if it were wrong the list would come back empty and every cluster
	// would be told its catalog serves no odf-operator. status.catalogSource is part of the
	// published API, and matching on it also disambiguates the several same-named odf-operator
	// manifests that appear when more than one catalog serves the package.
	list, err := mc.Dynamic.Resource(constants.PackageManifestGVR).Namespace(constants.OpenShiftMarketplaceNS).List(
		mc.Ctx, metav1.ListOptions{},
	)
	if err != nil {
		return "", "", false, fmt.Errorf(
			"list PackageManifests in %s: %w", constants.OpenShiftMarketplaceNS, err,
		)
	}

	pkg := findFdfOdfPackageManifest(list.Items)
	if pkg == nil {
		return "", "", false, nil
	}

	csvName, csvVersion, err = packageManifestChannelCSV(pkg, constants.OdfSubscriptionChannel)
	if err != nil {
		return "", "", false, err
	}
	return csvName, csvVersion, true, nil
}

// WaitForFdfOfferedOdfVersion resolves the odf-operator version offered by the FDF catalog,
// retrying while the package has not been published.
//
// A READY CatalogSource only means catalog-operator can reach the index pod; the PackageManifests
// are produced separately by the OLM packageserver on its own sync. Immediately after the catalog
// is created there is a window of seconds to minutes in which the package does not exist yet, and
// treating that as "the catalog has no odf-operator" would abort preflight with a misleading
// message on every first run.
//
// Errors from FdfOfferedOdfVersion are terminal and not retried: they mean the package was found
// but is unusable (no such channel, incomplete entry), which more waiting cannot fix.
func WaitForFdfOfferedOdfVersion(mc *kube.Context) (csvName, csvVersion string, err error) {
	timeout, interval := fdfPackageManifestWaitDurations(mc)

	name, version, found, err := FdfOfferedOdfVersion(mc)
	if err != nil {
		return "", "", err
	}
	if found {
		return name, version, nil
	}

	output.PrintInfo(fmt.Sprintf(
		"CatalogSource %s is READY but has not published the %q package yet; waiting up to %v...",
		constants.FDFCatalogSourceName, constants.OdfOperatorPackageName, timeout,
	))

	pollErr := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		n, v, ok, ferr := FdfOfferedOdfVersion(mc)
		if ferr != nil {
			return false, ferr
		}
		if !ok {
			return false, nil
		}
		name, version = n, v
		return true, nil
	}, timeout, interval,
		fmt.Sprintf(
			"PackageManifest %s from catalog %s",
			constants.OdfOperatorPackageName, constants.FDFCatalogSourceName,
		))

	if pollErr != nil {
		if errors.Is(pollErr, helpers.ErrPollDeadline) {
			return "", "", fmt.Errorf(
				"CatalogSource %s/%s is READY but did not publish a %q PackageManifest within %v; "+
					"check that FDF_CATALOG_IMAGE points at a Data Foundation catalog",
				constants.OpenShiftMarketplaceNS, constants.FDFCatalogSourceName,
				constants.OdfOperatorPackageName, timeout,
			)
		}
		return "", "", pollErr
	}
	return name, version, nil
}

func fdfPackageManifestWaitDurations(mc *kube.Context) (timeout, interval time.Duration) {
	timeout = constants.FDFPackageManifestWaitTimeout
	interval = constants.FDFPackageManifestWaitPollInterval
	if mc != nil && mc.FDFPackageManifestWaitTimeout > 0 && mc.FDFPackageManifestWaitPollInterval > 0 {
		timeout = mc.FDFPackageManifestWaitTimeout
		interval = mc.FDFPackageManifestWaitPollInterval
	}
	return timeout, interval
}

// findFdfOdfPackageManifest picks the odf-operator package served by the FDF catalog. Both the name
// and the owning catalog must match: several catalogs can serve a package called odf-operator, and
// an unset status.catalogSource means the packageserver has not finished populating the manifest,
// which is not a match.
func findFdfOdfPackageManifest(items []unstructured.Unstructured) *unstructured.Unstructured {
	for i := range items {
		item := &items[i]
		if item.GetName() != constants.OdfOperatorPackageName {
			continue
		}
		source, _, _ := unstructured.NestedString(item.Object, "status", "catalogSource")
		if source != constants.FDFCatalogSourceName {
			continue
		}
		return item
	}
	return nil
}

// packageManifestChannelCSV returns currentCSV and currentCSVDesc.version for the named channel.
func packageManifestChannelCSV(pkg *unstructured.Unstructured, channel string) (csvName, csvVersion string, err error) {
	channels, found, err := unstructured.NestedSlice(pkg.Object, "status", "channels")
	if err != nil {
		return "", "", fmt.Errorf("read status.channels on PackageManifest %s: %w", pkg.GetName(), err)
	}
	if !found || len(channels) == 0 {
		return "", "", fmt.Errorf(
			"PackageManifest %s from catalog %s reports no channels",
			pkg.GetName(), constants.FDFCatalogSourceName,
		)
	}

	available := make([]string, 0, len(channels))
	for _, raw := range channels {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(m, "name")
		if name == "" {
			continue
		}
		available = append(available, name)
		if name != channel {
			continue
		}
		csvName, _, _ = unstructured.NestedString(m, "currentCSV")
		csvVersion, _, _ = unstructured.NestedString(m, "currentCSVDesc", "version")
		if csvName == "" || csvVersion == "" {
			return "", "", fmt.Errorf(
				"PackageManifest %s channel %q is incomplete (currentCSV=%q currentCSVDesc.version=%q)",
				pkg.GetName(), channel, csvName, csvVersion,
			)
		}
		return csvName, csvVersion, nil
	}

	return "", "", fmt.Errorf(
		"PackageManifest %s from catalog %s has no channel %q (channels: %v)",
		pkg.GetName(), constants.FDFCatalogSourceName, channel, available,
	)
}
