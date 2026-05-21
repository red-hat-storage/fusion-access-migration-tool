package state

import (
	"context"
	"testing"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestContext() *kube.Context {
	return &kube.Context{
		Clientset:               fake.NewSimpleClientset(),
		Ctx:                     context.Background(),
		StateConfigMapName:      "migration-state",
		StateConfigMapNamespace: "ibm-spectrum-scale",
	}
}

func TestReadMigrationProgressDefaultsWhenMissing(t *testing.T) {
	mc := newTestContext()
	progress, err := ReadMigrationProgress(mc)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if progress.LastCompletedPhase != 0 {
		t.Fatalf("expected phase 0, got %d", progress.LastCompletedPhase)
	}
	if progress.Status != StatusInProgress {
		t.Fatalf("expected status %q, got %q", StatusInProgress, progress.Status)
	}
}

func TestWriteAndReadMigrationProgress(t *testing.T) {
	mc := newTestContext()
	expected := MigrationProgress{
		LastCompletedPhase:      3,
		SecureBootClusterForKMM: true,
		Status:                  StatusInProgress,
	}
	if err := WriteMigrationProgress(mc, expected); err != nil {
		t.Fatalf("write progress failed: %v", err)
	}

	actual, err := ReadMigrationProgress(mc)
	if err != nil {
		t.Fatalf("read progress failed: %v", err)
	}
	if actual.LastCompletedPhase != expected.LastCompletedPhase {
		t.Fatalf("phase mismatch: got %d want %d", actual.LastCompletedPhase, expected.LastCompletedPhase)
	}
	if actual.SecureBootClusterForKMM != expected.SecureBootClusterForKMM {
		t.Fatalf("secure boot mismatch: got %t want %t", actual.SecureBootClusterForKMM, expected.SecureBootClusterForKMM)
	}
}

func TestMarkMigrationFailedAndCompleted(t *testing.T) {
	mc := newTestContext()
	progress := MigrationProgress{
		LastCompletedPhase: 2,
		Status:             StatusInProgress,
	}
	if err := WriteMigrationProgress(mc, progress); err != nil {
		t.Fatalf("write progress failed: %v", err)
	}
	if err := MarkMigrationFailed(mc, progress, 3, "boom"); err != nil {
		t.Fatalf("mark failed failed: %v", err)
	}
	failed, err := ReadMigrationProgress(mc)
	if err != nil {
		t.Fatalf("read after failed mark failed: %v", err)
	}
	if failed.Status != StatusFailed || failed.FailedPhase != 3 {
		t.Fatalf("unexpected failed state: %+v", failed)
	}
	if err := MarkMigrationCompleted(mc, failed); err != nil {
		t.Fatalf("mark completed failed: %v", err)
	}
	completed, err := ReadMigrationProgress(mc)
	if err != nil {
		t.Fatalf("read after completed mark failed: %v", err)
	}
	if completed.Status != StatusCompleted || completed.LastCompletedPhase != 7 {
		t.Fatalf("unexpected completed state: %+v", completed)
	}
}
