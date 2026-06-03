package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestValidateBasicClusterHealth(t *testing.T) {
	t.Run("passes when clusterversion is not failing/degraded", func(t *testing.T) {
		mc := newValidationTestContext(clusterVersion("False", "False"))
		if err := ValidateBasicClusterHealth(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when clusterversion is failing", func(t *testing.T) {
		mc := newValidationTestContext(clusterVersion("True", "False"))
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
		mc := newValidationTestContext()
		err := ValidateScaleClusterExists(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no Scale clusters found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("passes when at least one scale cluster exists", func(t *testing.T) {
		mc := newValidationTestContext(scaleCluster("scale-cluster-a"))
		if err := ValidateScaleClusterExists(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})
}

func TestValidateScaleFilesystemHealthIfPresent(t *testing.T) {
	t.Run("skips when no filesystems exist", func(t *testing.T) {
		mc := newValidationTestContext()
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when filesystem is not mounted", func(t *testing.T) {
		mc := newValidationTestContext(filesystem("fs1", "Recovering", false))
		err := ValidateScaleFilesystemHealthIfPresent(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "fs1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("passes when all filesystems are mounted", func(t *testing.T) {
		mc := newValidationTestContext(
			filesystem("fs1", "Ready", true),
			filesystem("fs2", "Ready", true),
		)
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when filesystem uses status conditions Healthy instead of mounted", func(t *testing.T) {
		mc := newValidationTestContext(filesystemWithHealthyConditions("sharedfs", true))
		if err := ValidateScaleFilesystemHealthIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when Healthy condition is false", func(t *testing.T) {
		mc := newValidationTestContext(filesystemWithHealthyConditions("sharedfs", false))
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
		mc := newValidationTestContext()
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when localdisk phase is Ready", func(t *testing.T) {
		mc := newValidationTestContext(localDiskWithPhase("disk1", "Ready"))
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("passes when localdisk has condition Ready=True", func(t *testing.T) {
		mc := newValidationTestContext(localDiskWithReadyCondition("disk1"))
		if err := ValidateLocalDisksReadyIfPresent(mc); err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
	})

	t.Run("fails when localdisk is not ready", func(t *testing.T) {
		mc := newValidationTestContext(localDiskWithPhase("disk1", "Failed"))
		err := ValidateLocalDisksReadyIfPresent(mc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "disk1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func newValidationTestContext(objs ...*unstructured.Unstructured) *kube.Context {
	customListKinds := map[schema.GroupVersionResource]string{
		{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "clusters"}:    "ClusterList",
		{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "filesystems"}: "FilesystemList",
		{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "localdisks"}:  "LocalDiskList",
	}
	runtimeObjs := make([]runtime.Object, 0, len(objs))
	for _, obj := range objs {
		runtimeObjs = append(runtimeObjs, obj)
	}

	return &kube.Context{
		Ctx:       context.Background(),
		Clientset: kubefake.NewSimpleClientset(),
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
			runtime.NewScheme(),
			customListKinds,
			runtimeObjs...,
		),
	}
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
