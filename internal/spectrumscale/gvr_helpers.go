package spectrumscale

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

func resolveScaleClusterGVR(mc *kube.Context) (schema.GroupVersionResource, error) {
	gvr, err := helpers.ResolveGVR(mc.Ctx, mc.Dynamic, "scale.spectrum.ibm.com", "clusters", []string{"v1", "v1beta1"})
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf(
			"scale.spectrum.ibm.com Cluster API not found (cluster-scoped); tried v1 and v1beta1: %w", err)
	}
	return gvr, nil
}

func formatOwnerRefsForLog(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		ctrl := ""
		if r.Controller != nil && *r.Controller {
			ctrl = ",controller=true"
		}
		parts = append(parts, fmt.Sprintf("%s %s/%s (uid=%s%s)", r.APIVersion, r.Kind, r.Name, r.UID, ctrl))
	}
	return strings.Join(parts, "; ")
}

func retryableKubernetesGetErr(err error) bool {
	if apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTimeout(err) ||
		apierrors.IsInternalError(err) {
		return true
	}
	var status *apierrors.StatusError
	if errors.As(err, &status) && status.Status().Code >= 500 && status.Status().Code < 600 {
		return true
	}
	return false
}

func getUnstructuredWithRetries(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
) (*unstructured.Unstructured, error) {
	var lastErr error
	for i := 0; i < 6; i++ {
		obj, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
		if err == nil {
			return obj, nil
		}
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		if retryableKubernetesGetErr(err) {
			lastErr = err
			time.Sleep(time.Duration(150*(i+1)) * time.Millisecond)
			continue
		}
		return nil, err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("get %q after retries: %w", name, lastErr)
	}
	return nil, fmt.Errorf("get %q: unknown error", name)
}
