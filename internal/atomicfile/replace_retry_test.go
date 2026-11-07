package atomicfile

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestRetryReplaceRetriesTransientErrors(t *testing.T) {
	want := errors.New("transient replace failure")
	var calls atomic.Int32
	err := retryReplace(func() error {
		if calls.Add(1) < 4 {
			return want
		}
		return nil
	}, func(err error) bool { return errors.Is(err, want) })
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("replace called %d times, want 4", got)
	}
}

func TestRetryReplaceStopsOnPermanentError(t *testing.T) {
	want := errors.New("permanent replace failure")
	var calls atomic.Int32
	err := retryReplace(func() error {
		calls.Add(1)
		return want
	}, func(error) bool { return false })
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("replace called %d times, want 1", got)
	}
}

func TestRetryReplaceCapsAttempts(t *testing.T) {
	want := errors.New("persistent transient failure")
	var calls atomic.Int32
	err := retryReplace(func() error {
		calls.Add(1)
		return want
	}, func(err error) bool { return errors.Is(err, want) })
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if got := calls.Load(); got != 10 {
		t.Fatalf("replace called %d times, want 10", got)
	}
}
