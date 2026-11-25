package cluster

import (
	"fmt"
	"strings"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"

	"k8s.io/apimachinery/pkg/util/version"
)

// ParseFdfMajorMinor parses a ClusterServiceVersion spec.version (e.g. 4.20.1) into major and minor.
func ParseFdfMajorMinor(specVersion string) (major, minor uint64, err error) {
	if strings.TrimSpace(specVersion) == "" {
		return 0, 0, fmt.Errorf("empty CSV spec.version")
	}
	v, err := version.ParseSemantic(specVersion)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid CSV spec.version %q: %w", specVersion, err)
	}
	return uint64(v.Major()), uint64(v.Minor()), nil
}

// OdfRedHatPreflightAllowed validates Red Hat ODF odf-operator CSV spec.version for preflight.
// Red Hat ODF 4.20.x and 4.21.x are accepted as valid starting points for migration to IBM FDF.
//
// This covers the minor only. Acceptance is additionally gated by FdfCatalogCanReplaceOdf, which
// compares the installed version against what the FDF catalog actually offers.
func OdfRedHatPreflightAllowed(specVersion string) error {
	major, minor, err := ParseFdfMajorMinor(specVersion)
	if err != nil {
		return err
	}
	if major != constants.RequiredFDFMajor {
		return fmt.Errorf("unsupported CSV spec.version major (want %d.x, got %d.%d)", constants.RequiredFDFMajor, major, minor)
	}
	if minor != 20 && minor != 21 {
		return fmt.Errorf("unsupported Red Hat ODF minor %d (migration supports 4.20.x or 4.21.x)", minor)
	}
	return nil
}

// FdfCatalogCanReplaceOdf reports whether OLM can move the installed odf-operator CSV onto the
// version the FDF catalog offers. OLM resolves upgrades by CSV name, and Red Hat and IBM builds
// share the odf-operator.vX.Y.Z naming scheme, so the FDF catalog head must be strictly greater
// than the installed version:
//
//   - offered > installed: OLM generates an InstallPlan and the IBM CSV replaces the Red Hat one.
//   - offered == installed: the CSV name already exists, OLM considers it installed and does
//     nothing; the Red Hat CSV silently stays.
//   - offered < installed: OLM does not downgrade; the Red Hat CSV silently stays.
//
// This tool blocks rather than forcing the swap by deleting CSVs, so the remedy for the last two
// cases is an FDF release above the installed ODF version.
func FdfCatalogCanReplaceOdf(installedODF, fdfOffered string) error {
	installed, err := version.ParseSemantic(strings.TrimSpace(installedODF))
	if err != nil {
		return fmt.Errorf("invalid installed ODF version %q: %w", installedODF, err)
	}
	offered, err := version.ParseSemantic(strings.TrimSpace(fdfOffered))
	if err != nil {
		return fmt.Errorf("invalid FDF catalog version %q: %w", fdfOffered, err)
	}

	if offered.GreaterThan(installed) {
		return nil
	}

	reason := "OLM does not downgrade an installed operator"
	if offered.EqualTo(installed) {
		reason = "OLM resolves upgrades by CSV name, so an identical version is treated as already installed"
	}
	return fmt.Errorf(
		"installed ODF %s is not below the FDF catalog version %s: %s, so the Red Hat CSVs would not be "+
			"replaced and the migration would leave ODF in place. Migrate once an FDF release above %s is "+
			"available in the catalog (this tool will not downgrade ODF)",
		installed, offered, reason, installed,
	)
}

// FdfOdfPreflightAllowed validates IBM FDF odf-operator CSV spec.version for preflight.
// IBM FDF 4.20.x is always allowed. IBM FDF 4.21.x is allowed only when resumingFromCheckpoint is true
// (migration resumed from checkpoint after install phase may have upgraded the operator).
func FdfOdfPreflightAllowed(specVersion string, resumingFromCheckpoint bool) error {
	major, minor, err := ParseFdfMajorMinor(specVersion)
	if err != nil {
		return err
	}
	if major != constants.RequiredFDFMajor {
		return fmt.Errorf("unsupported CSV spec.version major (want %d.x, got %d.%d)", constants.RequiredFDFMajor, major, minor)
	}
	switch minor {
	case 20:
		return nil
	case 21:
		if !resumingFromCheckpoint {
			return fmt.Errorf(
				"IBM FDF 4.21.x is installed; migration preflight requires IBM FDF 4.20.x on a fresh run. " +
					"If FDF was upgraded by a previous migration phase, resume from checkpoint so preflight is skipped",
			)
		}
		return nil
	default:
		return fmt.Errorf("unsupported IBM FDF minor %d (migration supports 4.20.x; 4.21.x is accepted only when resuming)", minor)
	}
}
