package spectrumscale

import (
	"errors"
	"fmt"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// waitForDynamicResourceGone polls until Get returns NotFound, clearing finalizers when present on each poll.
func waitForDynamicResourceGone(
	mc *kube.Context,
	res dynamic.ResourceInterface,
	name string,
	resourceType string,
	timeout, poll time.Duration,
	pollDescription string,
	stickyFinalizersParenthetical string,
	clearFinalizers func() error,
	warnClearFailure func(err error),
	deadlineExceeded func(timeout time.Duration, finalizers []string) error,
) error {
	var loggedSticky bool
	err := helpers.PollUntil(mc.Ctx, func() (bool, error) {
		obj, err := res.Get(mc.Ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("get %s %q: %w", resourceType, name, err)
		}
		if len(obj.GetFinalizers()) > 0 {
			if !loggedSticky {
				msg := fmt.Sprintf(
					"%s %q still has finalizers %v — clearing on each poll until removed",
					resourceType, name, obj.GetFinalizers(),
				)
				if stickyFinalizersParenthetical != "" {
					msg += " " + stickyFinalizersParenthetical
				}
				output.PrintInfo(msg)
				loggedSticky = true
			}
			if err := clearFinalizers(); err != nil {
				warnClearFailure(err)
			}
		}
		return false, nil
	}, timeout, poll, pollDescription)
	if err != nil && errors.Is(err, helpers.ErrPollDeadline) {
		last, gerr := res.Get(mc.Ctx, name, metav1.GetOptions{})
		fin := []string(nil)
		if gerr == nil && last != nil {
			fin = last.GetFinalizers()
		}
		return deadlineExceeded(timeout, fin)
	}
	return err
}
