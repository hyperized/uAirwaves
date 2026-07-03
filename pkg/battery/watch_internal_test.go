package battery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fastTick     = time.Millisecond
	waitDeadline = 2 * time.Second
)

// errFakeRead is a static sentinel for the fake reader failure
// path (err113 wants wrapped static errors, not inline ones).
var errFakeRead = errors.New("fake read failure")

// staticReader yields a fixed reading and error on every call. The
// error parameter varies across callers (nil / sentinel / unsupported)
// so it exercises all of applyOnce's branches from one helper.
func staticReader(got reading, err error) reader {
	return func(context.Context) (reading, error) { return got, err }
}

// countingReader wraps a reader, recording how many times it was
// invoked so a test can prove the ticker branch fired without
// narrowing the count into the reading itself.
func countingReader(inner reader, calls *atomic.Int64) reader {
	return func(ctx context.Context) (reading, error) {
		calls.Add(1)

		return inner(ctx)
	}
}

// waitFor polls cond until it holds or the deadline elapses, so
// loop-driven assertions don't race the background watcher.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}

		time.Sleep(time.Millisecond)
	}

	return cond()
}

func TestApplyOnce(t *testing.T) {
	t.Parallel()

	t.Run("success folds reading into status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()

		if applyOnce(context.Background(), status, staticReader(reading{percentage: 42, charging: true}, nil)) {
			t.Fatal("success read must not stop the loop")
		}

		if status.GetPercentage() != 42 || !status.IsCharging() {
			t.Errorf("status not updated: got %s", status.String())
		}
	})

	t.Run("transient error keeps polling and leaves status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus(WithPercentage(7))

		if applyOnce(context.Background(), status, staticReader(reading{}, errFakeRead)) {
			t.Fatal("transient error must not stop the loop")
		}

		if status.GetPercentage() != 7 {
			t.Errorf("status changed on error: got %d", status.GetPercentage())
		}
	})

	t.Run("unsupported stops the loop", func(t *testing.T) {
		t.Parallel()

		if !applyOnce(context.Background(), NewStatus(), staticReader(reading{}, ErrUnsupported)) {
			t.Fatal("ErrUnsupported must stop the loop")
		}
	})
}

func TestWatchWith_UnsupportedReturnsImmediately(t *testing.T) {
	t.Parallel()

	// A non-cancelled context with an hour-long interval would hang
	// forever if the unsupported source did not short-circuit.
	read := staticReader(reading{}, ErrUnsupported)

	if err := watchWith(context.Background(), NewStatus(), time.Hour, read); err != nil {
		t.Errorf("expected nil on unsupported source, got %v", err)
	}
}

func TestWatchWith_UpdatesAcrossTicksThenCancel(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	// A call count >= 2 proves the ticker branch ran, not just the
	// initial pre-loop read.
	read := countingReader(staticReader(reading{percentage: 80, charging: true}, nil), &calls)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	status := NewStatus()
	errCh := make(chan error, 1)

	go func() { errCh <- watchWith(ctx, status, fastTick, read) }()

	if !waitFor(t, func() bool { return calls.Load() >= 2 }) {
		t.Fatal("watcher did not tick")
	}

	if status.GetPercentage() != 80 {
		t.Errorf("expected percentage 80, got %d", status.GetPercentage())
	}

	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("expected nil on cancel, got %v", err)
	}
}

func TestWatchWith_BecomesUnsupportedMidLoop(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	// First read succeeds so the loop starts; the next read reports
	// unsupported from the ticker branch, which must end the loop on
	// its own without a context cancel.
	read := func(context.Context) (reading, error) {
		if calls.Add(1) == 1 {
			return reading{percentage: 10}, nil
		}

		return reading{}, ErrUnsupported
	}

	if err := watchWith(context.Background(), NewStatus(), fastTick, read); err != nil {
		t.Errorf("expected nil when source turns unsupported, got %v", err)
	}
}

func TestWatchWith_TransientErrorThenRecovers(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	read := func(context.Context) (reading, error) {
		if calls.Add(1) == 1 {
			return reading{}, errFakeRead
		}

		return reading{percentage: 55, charging: true}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	status := NewStatus()
	errCh := make(chan error, 1)

	go func() { errCh <- watchWith(ctx, status, fastTick, read) }()

	if !waitFor(t, func() bool { return status.GetPercentage() == 55 }) {
		t.Fatal("watcher did not recover after transient error")
	}

	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("expected nil on cancel, got %v", err)
	}
}
