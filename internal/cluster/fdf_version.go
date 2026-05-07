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
