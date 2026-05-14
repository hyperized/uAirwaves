package ui_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
)

var (
	errWorkerSentinel = errors.New("worker sentinel")
	errSynthetic      = errors.New("synthetic worker failure")
	errPanicRecover   = errors.New("recovered in synthetic worker")
)

// TestLaunchWorkerForwardsError covers the standard error-return
// path: a worker that returns a non-nil error must surface it
// verbatim on errChan.
func TestLaunchWorkerForwardsError(t *testing.T) {
	t.Parallel()

	var waitGroup sync.WaitGroup

	errChan := make(chan error, 1)

	ui.LaunchWorker(&waitGroup, errChan, errWorkerSentinel, func() error {
		return errSynthetic
	})

	waitGroup.Wait()

	select {
	case got := <-errChan:
		if !errors.Is(got, errSynthetic) {
			t.Errorf("errChan got %v, want errSynthetic", got)
		}
	case <-time.After(time.Second):
		t.Fatal("worker error not forwarded within deadline")
	}
}

// TestLaunchWorkerNilErrorSilent covers the "no error" path: a
// worker returning nil must not produce a value on errChan.
func TestLaunchWorkerNilErrorSilent(t *testing.T) {
	t.Parallel()

	var waitGroup sync.WaitGroup

	errChan := make(chan error, 1)

	ui.LaunchWorker(&waitGroup, errChan, errWorkerSentinel, func() error {
		return nil
	})

	waitGroup.Wait()

	select {
	case got := <-errChan:
		t.Errorf("errChan unexpectedly received %v", got)
	default:
	}
}

// TestLaunchWorkerRecoversPanicWithError covers the panic-recovery
// path where the panic value is an error: it must be joined with
// the sentinel so errors.Is reaches both.
func TestLaunchWorkerRecoversPanicWithError(t *testing.T) {
	t.Parallel()

	var waitGroup sync.WaitGroup

	errChan := make(chan error, 1)

	ui.LaunchWorker(&waitGroup, errChan, errPanicRecover, func() error {
		panic(errSynthetic)
	})

	waitGroup.Wait()

	select {
	case got := <-errChan:
		if !errors.Is(got, errSynthetic) {
			t.Errorf("errChan got %v, want errSynthetic in join", got)
		}

		if !errors.Is(got, errPanicRecover) {
			t.Errorf("errChan got %v, want errPanicRecover in join", got)
		}
	case <-time.After(time.Second):
		t.Fatal("panic not recovered within deadline")
	}
}

// TestLaunchWorkerRecoversPanicWithNonError covers the recovery
// path where the panic value is not an error: the type assertion
// fails and nothing is forwarded — the goroutine simply exits.
// We must observe wg.Wait() returning without errChan firing.
func TestLaunchWorkerRecoversPanicWithNonError(t *testing.T) {
	t.Parallel()

	var waitGroup sync.WaitGroup

	errChan := make(chan error, 1)

	ui.LaunchWorker(&waitGroup, errChan, errPanicRecover, func() error {
		panic("not an error")
	})

	waitGroup.Wait()

	select {
	case got := <-errChan:
		t.Errorf("non-error panic should not forward; got %v", got)
	default:
	}
}
