package helpers

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPollDeadline indicates PollUntil stopped because the timeout was reached.
var ErrPollDeadline = errors.New("poll deadline exceeded")

// Condition returns done=true when the waited-for state is reached.
type Condition func() (done bool, err error)

// PollUntil polls condition until it returns true, the timeout expires, or ctx is cancelled.
func PollUntil(ctx context.Context, condition Condition, timeout, interval time.Duration, description string) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for %s: %w", description, ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("%w: after %v waiting for %s", ErrPollDeadline, timeout, description)
			}
			done, err := condition()
			if err != nil {
				return fmt.Errorf("error while waiting for %s: %w", description, err)
			}
			if done {
				return nil
			}
		}
	}
}
