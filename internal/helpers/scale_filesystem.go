package helpers

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// scaleFilesystemConditionStatus returns status string and true if a status.conditions entry exists for condType.
func scaleFilesystemConditionStatus(fs *unstructured.Unstructured, condType string) (status string, found bool) {
	conds, ok, err := unstructured.NestedSlice(fs.Object, "status", "conditions")
	if !ok || err != nil {
		return "", false
	}
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		if !strings.EqualFold(t, condType) {
			continue
		}
		s, _, _ := unstructured.NestedString(m, "status")
		return s, true
	}
	return "", false
}

// ScaleFilesystemReportsHealthy reports whether a filesystems.scale.spectrum.ibm.com object is healthy.
// Newer IBM / Fusion CRs use status.conditions (e.g. type Healthy) instead of legacy status.mounted.
func ScaleFilesystemReportsHealthy(fs *unstructured.Unstructured) (ok bool, detail string) {
	mounted, mountedFound, _ := unstructured.NestedBool(fs.Object, "status", "mounted")

	healthyStatus, healthyFound := scaleFilesystemConditionStatus(fs, "Healthy")
	if healthyFound && strings.EqualFold(healthyStatus, "False") {
		return false, "Healthy=False"
	}

	if mountedFound && mounted {
		return true, "mounted=true"
	}
	if healthyFound && strings.EqualFold(healthyStatus, "True") {
		return true, "Healthy=True"
	}
	if mountedFound && !mounted {
		return false, "mounted=false"
	}

	phase, _, _ := unstructured.NestedString(fs.Object, "status", "phase")
	if phase == "Ready" {
		return true, "phase=Ready"
	}

	if healthyFound {
		return false, "Healthy=" + healthyStatus
	}
	if phase != "" {
		return false, "phase=" + phase
	}
	return false, "no mounted/Healthy/phase status"
}
