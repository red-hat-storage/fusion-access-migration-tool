package helpers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPollUntil_Success(t *testing.T) {
	ctx := context.Background()
	called := 0
	condition := func() (bool, error) {
		called++
		if called >= 3 {
			return true, nil
		}
		return false, nil
	}

	err := PollUntil(ctx, condition, 5*time.Second, 10*time.Millisecond, "test condition")
	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}
	if called < 3 {
		t.Errorf("expected at least 3 calls, got %d", called)
	}
}

func TestPollUntil_Timeout(t *testing.T) {
	ctx := context.Background()
	condition := func() (bool, error) {
		return false, nil
	}

	err := PollUntil(ctx, condition, 50*time.Millisecond, 10*time.Millisecond, "test timeout")
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrPollDeadline) {
		t.Errorf("expected ErrPollDeadline, got %v", err)
	}
}

func TestPollUntil_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	condition := func() (bool, error) {
		return false, nil
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := PollUntil(ctx, condition, 5*time.Second, 10*time.Millisecond, "test cancel")
	if err == nil {
		t.Error("expected context cancelled error, got nil")
	}
}
