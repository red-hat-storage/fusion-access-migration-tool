package helpers

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const clearFinalizersMergePatch = `{"metadata":{"finalizers":[]}}`

// ClearFinalizers removes all finalizers from a resource using merge patch, with conflict retry.
func ClearFinalizers(
	ctx context.Context,
	res dynamic.ResourceInterface,
	name string,
	resourceType string,
	maxAttempts int,
) error {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		obj, err := res.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get %s %q: %w", resourceType, name, err)
		}
		if len(obj.GetFinalizers()) == 0 {
			return nil
		}

		_, perr := res.Patch(ctx, name, types.MergePatchType, []byte(clearFinalizersMergePatch), metav1.PatchOptions{})
		if perr == nil || apierrors.IsNotFound(perr) {
			return nil
		}

		if !apierrors.IsConflict(perr) {
			obj.SetFinalizers([]string{})
			_, uerr := res.Update(ctx, obj, metav1.UpdateOptions{})
			if uerr == nil || apierrors.IsNotFound(uerr) {
				return nil
			}
			if !apierrors.IsConflict(uerr) {
				return fmt.Errorf("clear finalizers on %s %q: patch: %v; update: %w", resourceType, name, perr, uerr)
			}
		}

		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("clear finalizers on %s %q: exhausted %d retries", resourceType, name, maxAttempts)
}
