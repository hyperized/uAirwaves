package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// logCaptureMu serialises the tests that swap the process-global
// slog default so their buffer assertions stay deterministic under
// t.Parallel(). It does not stop other parallel tests (e.g. the
// bias-tee adapter tests) from logging through the default handler
// while it is swapped in — syncBuffer guards the bytes against those
// concurrent writes.
//
//nolint:gochecknoglobals // must be package-level to serialise the global-default swap across parallel tests.
var logCaptureMu sync.Mutex

// errSimulatedRun is the sentinel a run func returns to exercise the
// error branch of runWithRecover.
var errSimulatedRun = errors.New("simulated tview failure")

// syncBuffer is a bytes.Buffer safe for concurrent Write/String, so a
// parallel test logging through the swapped-in default handler cannot
// race the assertion read.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // io.Writer adapter returns the underlying write error verbatim.
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// runCaptured swaps a buffer-backed slog handler in as the process
// default, runs runWithRecover, restores the prior default, and
// returns the exit code plus everything logged during the call.
func runCaptured(t *testing.T, run func() error, restoreLog func()) (int, string) {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	prev := slog.Default()
	defer slog.SetDefault(prev)

	buf := &syncBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	code := runWithRecover(run, restoreLog)

	return code, buf.String()
}

// TestRunWithRecoverPanicReturnsOne asserts a panicking run func is
// recovered: exit code 1, restoreLog invoked, panic value + stack
// logged.
func TestRunWithRecoverPanicReturnsOne(t *testing.T) {
	t.Parallel()

	var restoreCalled bool

	code, logged := runCaptured(t,
		func() error { panic("radar boom") },
		func() { restoreCalled = true },
	)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !restoreCalled {
		t.Error("restoreLog was not called on the panic path")
	}

	if !strings.Contains(logged, "radar boom") {
		t.Errorf("panic value not logged; got %q", logged)
	}

	if !strings.Contains(logged, "stack=") {
		t.Errorf("stack trace not logged; got %q", logged)
	}
}

// TestRunWithRecoverNilErrorReturnsZero asserts the clean path: exit
// 0, no tview error logged, and restoreLog left for main to call.
func TestRunWithRecoverNilErrorReturnsZero(t *testing.T) {
	t.Parallel()

	code, logged := runCaptured(t,
		func() error { return nil },
		func() { t.Error("restoreLog must not run on the clean path") },
	)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if strings.Contains(logged, "tview error") {
		t.Errorf("clean run logged a tview error: %q", logged)
	}
}

// TestRunWithRecoverRunErrorReturnsZero asserts a run func returning
// an error still exits 0 (graceful) and logs the tview error,
// preserving today's behaviour.
func TestRunWithRecoverRunErrorReturnsZero(t *testing.T) {
	t.Parallel()

	code, logged := runCaptured(t,
		func() error { return errSimulatedRun },
		func() { t.Error("restoreLog must not run on the error path") },
	)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if !strings.Contains(logged, "tview error") {
		t.Errorf("run error was not logged: %q", logged)
	}

	if !strings.Contains(logged, errSimulatedRun.Error()) {
		t.Errorf("run error text missing from log: %q", logged)
	}
}
