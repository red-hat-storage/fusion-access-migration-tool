package cluster

import (
	"strings"
	"testing"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestValidateBasicClusterHealth(t *testing.T) {
	t.Run("passes when clusterversion is not failing/degraded", func(t *testing.T) {
		mc := newClusterTestContext(nil, clusterVersion("False", "False"))
		if err := ValidateBasicClusterHealth(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when clusterversion is failing", func(t *testing.T) {
		mc := newClusterTestContext(nil, clusterVersion("True", "False"))
		err := ValidateBasicClusterHealth(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cluster is not healthy") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateScaleClusterExists(t *testing.T) {
	t.Run("fails when no scale clusters exist", func(t *testing.T) {
		mc := newClusterTestContext(nil)
		err := ValidateScaleClusterExists(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no Scale clusters found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("passes when at least one scale cluster exists", func(t *testing.T) {
		mc := newClusterTestContext(nil, scaleCluster("scale-cluster-a"))
		if err := ValidateScaleClusterExists(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})
}

func TestValidateScaleFilesystemHealthIfPresent(t *testing.T) {
	t.Run("skips when no filesystems exist", func(t *testing.T) {
		mc := newClusterTestContext(nil)
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when filesystem is not mounted", func(t *testing.T) {
		mc := newClusterTestContext(nil, filesystem("fs1", "Recovering", false))
		err := ValidateScaleFilesystemHealthIfPresent(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "fs1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("passes when all filesystems are mounted", func(t *testing.T) {
		mc := newClusterTestContext(nil,
			filesystem("fs1", "Ready", true),
			filesystem("fs2", "Ready", true),
		)
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when filesystem uses status conditions Healthy instead of mounted", func(t *testing.T) {
		mc := newClusterTestContext(nil, filesystemWithHealthyConditions("sharedfs", true))
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when Healthy condition is false", func(t *testing.T) {
		mc := newClusterTestContext(nil, filesystemWithHealthyConditions("sharedfs", false))
		err := ValidateScaleFilesystemHealthIfPresent(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "sharedfs") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateLocalDisksReadyIfPresent(t *testing.T) {
	t.Run("skips when no localdisks exist", func(t *testing.T) {
		mc := newClusterTestContext(nil)
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when localdisk phase is Ready", func(t *testing.T) {
		mc := newClusterTestContext(nil, localDiskWithPhase("disk1", "Ready"))
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when localdisk has condition Ready=True", func(t *testing.T) {
		mc := newClusterTestContext(nil, localDiskWithReadyCondition("disk1"))
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when localdisk is not ready", func(t *testing.T) {
		mc := newClusterTestContext(nil, localDiskWithPhase("disk1", "Failed"))
		err := ValidateLocalDisksReadyIfPresent(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "disk1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func clusterVersion(failing string, degraded string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.ClusterVersionGVR.GroupVersion().String(),
			"kind":       "ClusterVersion",
			"metadata": map[string]interface{}{
				"name": "version",
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Failing",
						"status": failing,
					},
					map[string]interface{}{
						"type":   "Degraded",
						"status": degraded,
					},
				},
			},
		},
	}
}

func scaleCluster(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "scale.spectrum.ibm.com/v1beta1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.SpectrumScaleNS,
			},
		},
	}
}

func filesystem(name string, phase string, mounted bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "scale.spectrum.ibm.com/v1beta1",
			"kind":       "Filesystem",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.SpectrumScaleNS,
			},
			"status": map[string]interface{}{
				"phase":   phase,
				"mounted": mounted,
			},
		},
	}
}

func filesystemWithHealthyConditions(name string, healthyTrue bool) *unstructured.Unstructured {
	healthyStatus := "False"
	if healthyTrue {
		healthyStatus = "True"
	}
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "scale.spectrum.ibm.com/v1beta1",
			"kind":       "Filesystem",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.SpectrumScaleNS,
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Success", "status": "True", "reason": "Created"},
					map[string]interface{}{"type": "Healthy", "status": healthyStatus, "reason": "Healthy"},
				},
			},
		},
	}
}

func localDiskWithPhase(name string, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "scale.spectrum.ibm.com/v1beta1",
			"kind":       "LocalDisk",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.SpectrumScaleNS,
			},
			"status": map[string]interface{}{
				"phase": phase,
			},
		},
	}
}

func localDiskWithReadyCondition(name string) *unstructured.Unstructured {
	obj := localDiskWithPhase(name, "Pending").DeepCopy()
	_ = unstructured.SetNestedSlice(
		obj.Object,
		[]interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": "True",
			},
		},
		"status",
		"conditions",
	)
	return obj
}
