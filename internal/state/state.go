package state

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
)

const MigrationProgressDataKey = "migration-progress.json"

const (
	StatusInProgress = "in_progress"
	StatusFailed     = "failed"
	StatusCompleted  = "completed"
)

type MigrationProgress struct {
	LastCompletedPhase      int    `json:"lastCompletedPhase"`
	SecureBootClusterForKMM bool   `json:"secureBootClusterForKMM,omitempty"`
	Status                  string `json:"status,omitempty"`
	FailedPhase             int    `json:"failedPhase,omitempty"`
	FailureReason           string `json:"failureReason,omitempty"`
}

func defaultProgress() MigrationProgress {
	return MigrationProgress{
		Status: StatusInProgress,
	}
}

func validateProgress(progress MigrationProgress) error {
	if progress.LastCompletedPhase < 0 || progress.LastCompletedPhase > 6 {
		return fmt.Errorf("invalid lastCompletedPhase %d", progress.LastCompletedPhase)
	}
	if progress.FailedPhase < 0 || progress.FailedPhase > 6 {
		return fmt.Errorf("invalid failedPhase %d", progress.FailedPhase)
	}
	switch progress.Status {
	case "", StatusInProgress, StatusFailed, StatusCompleted:
	default:
		return fmt.Errorf("invalid status %q", progress.Status)
	}
	return nil
}

func validateContext(mc *kube.Context) error {
	if mc == nil {
		return fmt.Errorf("migration context is nil")
	}
	if mc.StateConfigMapNamespace == "" {
		return fmt.Errorf("state configmap namespace is required")
	}
	if mc.StateConfigMapName == "" {
		return fmt.Errorf("state configmap name is required")
	}
	return nil
}

func ensureConfigMap(mc *kube.Context) (*corev1.ConfigMap, error) {
	configMaps := mc.Clientset.CoreV1().ConfigMaps(mc.StateConfigMapNamespace)
	cm, err := configMaps.Get(mc.Ctx, mc.StateConfigMapName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("get state configmap %s/%s: %w", mc.StateConfigMapNamespace, mc.StateConfigMapName, err)
		}
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mc.StateConfigMapName,
				Namespace: mc.StateConfigMapNamespace,
			},
			Data: map[string]string{},
		}
		cm, err = configMaps.Create(mc.Ctx, cm, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				cm, err = configMaps.Get(mc.Ctx, mc.StateConfigMapName, metav1.GetOptions{})
				if err != nil {
					return nil, fmt.Errorf("get state configmap %s/%s after create race: %w", mc.StateConfigMapNamespace, mc.StateConfigMapName, err)
				}
			} else {
				return nil, fmt.Errorf("create state configmap %s/%s: %w", mc.StateConfigMapNamespace, mc.StateConfigMapName, err)
			}
		}
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	return cm, nil
}

func ReadMigrationProgress(mc *kube.Context) (MigrationProgress, error) {
	if err := validateContext(mc); err != nil {
		return MigrationProgress{}, err
	}
	cm, err := mc.Clientset.CoreV1().ConfigMaps(mc.StateConfigMapNamespace).Get(mc.Ctx, mc.StateConfigMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return defaultProgress(), nil
		}
		return MigrationProgress{}, fmt.Errorf("get state configmap %s/%s: %w", mc.StateConfigMapNamespace, mc.StateConfigMapName, err)
	}
	raw := cm.Data[MigrationProgressDataKey]
	if raw == "" {
		return defaultProgress(), nil
	}

	var progress MigrationProgress
	if err := json.Unmarshal([]byte(raw), &progress); err != nil {
		return MigrationProgress{}, fmt.Errorf(
			"parse state configmap key %s in %s/%s: %w",
			MigrationProgressDataKey,
			mc.StateConfigMapNamespace,
			mc.StateConfigMapName,
			err,
		)
	}
	if err := validateProgress(progress); err != nil {
		return MigrationProgress{}, err
	}
	if progress.Status == "" {
		progress.Status = StatusInProgress
	}
	return progress, nil
}

func WriteMigrationProgress(mc *kube.Context, progress MigrationProgress) error {
	if err := validateContext(mc); err != nil {
		return err
	}
	if err := validateProgress(progress); err != nil {
		return err
	}
	if progress.Status == "" {
		progress.Status = StatusInProgress
	}

	cm, err := ensureConfigMap(mc)
	if err != nil {
		return err
	}
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("marshal migration progress: %w", err)
	}

	cm.Data[MigrationProgressDataKey] = string(data)
	_, err = mc.Clientset.CoreV1().ConfigMaps(mc.StateConfigMapNamespace).Update(mc.Ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update state configmap %s/%s: %w", mc.StateConfigMapNamespace, mc.StateConfigMapName, err)
	}
	return nil
}

func MarkMigrationCompleted(mc *kube.Context, progress MigrationProgress) error {
	progress.LastCompletedPhase = 6
	progress.Status = StatusCompleted
	progress.FailedPhase = 0
	progress.FailureReason = ""
	return WriteMigrationProgress(mc, progress)
}

func MarkMigrationFailed(mc *kube.Context, progress MigrationProgress, failedPhase int, reason string) error {
	progress.Status = StatusFailed
	progress.FailedPhase = failedPhase
	progress.FailureReason = reason
	return WriteMigrationProgress(mc, progress)
}
