package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// stubBiasStream implements biasTeeStream for adapter tests. State
// and set calls are counted under a mutex so the worker goroutine
// and the test goroutine race-free. setEntered / blockSet give the
// in-flight and returns-immediately tests deterministic control over
// when SetBiasTee is entered and when it completes.
type stubBiasStream struct {
	mu        sync.Mutex
	supported bool
	enabled   bool
	setErr    error
	setCalls  int
	lastSet   bool

	// setEntered, when non-nil, is closed the first time SetBiasTee
	// is entered so a test can wait for the worker to reach the
	// control transfer before pressing again.
	setEntered chan struct{}

	// blockSet, when non-nil, holds SetBiasTee until the test closes
	// it — used to pin a toggle worker in flight.
	blockSet chan struct{}
}

func (s *stubBiasStream) BiasTeeState() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.supported, s.enabled
}

func (s *stubBiasStream) SetBiasTee(enable bool) error {
	s.mu.Lock()
	s.setCalls++
	s.lastSet = enable
	entered := s.setEntered
	block := s.blockSet
	err := s.setErr
	s.mu.Unlock()

	if entered != nil {
		close(entered)
	}

	if block != nil {
		<-block
	}

	return err
}

//nolint:nonamedreturns // (setCalls, lastSet) reads clearer named at this signature.
func (s *stubBiasStream) snapshot() (setCalls int, lastSet bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.setCalls, s.lastSet
}

var errStubBiasSet = errors.New("stub bias-tee set failure")

// newAdapter wires a stub into a biasTeeAdapter with a real
// WaitGroup and a buffered error channel, so tests can join the
// dispatched worker via wg.Wait and assert nothing landed on
// errChan.
func newAdapter(stub *stubBiasStream) (*biasTeeAdapter, *sync.WaitGroup, chan error) {
	var waitGroup sync.WaitGroup

	errChan := make(chan error, 1)
	adapter := &biasTeeAdapter{
		stream:    stub,
		waitGroup: &waitGroup,
		errChan:   errChan,
	}

	return adapter, &waitGroup, errChan
}

// TestToggleBiasTeeUnsupportedNoOps exercises the worker's
// early-return branch when the active source has no bias-tee
// surface: the GPIO is never driven and nothing lands on errChan.
func TestToggleBiasTeeUnsupportedNoOps(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: false}
	adapter, waitGroup, errChan := newAdapter(stub)

	adapter.ToggleBiasTee()
	waitGroup.Wait()

	if setCalls, _ := stub.snapshot(); setCalls != 0 {
		t.Errorf("setCalls = %d, want 0 (unsupported must not drive the GPIO)", setCalls)
	}

	if len(errChan) != 0 {
		t.Errorf("errChan len = %d, want 0 (unsupported is a benign no-op)", len(errChan))
	}
}

// TestToggleBiasTeeSuccessFlipsBit covers the happy path in both
// directions: the worker computes the target from the cached state
// and drives SetBiasTee to the opposite bit.
func TestToggleBiasTeeSuccessFlipsBit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initialState bool
		wantSet      bool
	}{
		{name: "off to on", initialState: false, wantSet: true},
		{name: "on to off", initialState: true, wantSet: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubBiasStream{supported: true, enabled: testCase.initialState}
			adapter, waitGroup, errChan := newAdapter(stub)

			adapter.ToggleBiasTee()
			waitGroup.Wait()

			setCalls, lastSet := stub.snapshot()
			if setCalls != 1 {
				t.Fatalf("setCalls = %d, want 1", setCalls)
			}

			if lastSet != testCase.wantSet {
				t.Errorf("lastSet = %v, want %v", lastSet, testCase.wantSet)
			}

			if len(errChan) != 0 {
				t.Errorf("errChan len = %d, want 0", len(errChan))
			}
		})
	}
}

// TestToggleBiasTeeSetFailureNoErrChan asserts a failed toggle logs
// and clears the in-flight guard without killing the app: errChan
// stays empty and a subsequent press launches a fresh worker.
func TestToggleBiasTeeSetFailureNoErrChan(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: true, enabled: false, setErr: errStubBiasSet}
	adapter, waitGroup, errChan := newAdapter(stub)

	adapter.ToggleBiasTee()
	waitGroup.Wait()

	if len(errChan) != 0 {
		t.Fatalf("errChan len = %d, want 0 (a failed toggle must not kill the app)", len(errChan))
	}

	setCalls, lastSet := stub.snapshot()
	if setCalls != 1 || !lastSet {
		t.Errorf("set = (%d, %v), want (1, true) — off → on still attempted", setCalls, lastSet)
	}

	// inFlight must be cleared so the next press starts a new worker.
	adapter.ToggleBiasTee()
	waitGroup.Wait()

	if setCalls, _ := stub.snapshot(); setCalls != 2 {
		t.Errorf("setCalls after second press = %d, want 2 (inFlight not cleared?)", setCalls)
	}
}

// TestToggleBiasTeeReturnsImmediately asserts ToggleBiasTee does no
// USB work on the calling (event-loop) goroutine: with SetBiasTee
// pinned mid-transfer, the call still returns well within the
// deadline.
func TestToggleBiasTeeReturnsImmediately(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: true, blockSet: make(chan struct{})}
	adapter, waitGroup, _ := newAdapter(stub)

	returned := make(chan struct{})

	go func() {
		adapter.ToggleBiasTee()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("ToggleBiasTee blocked on the USB control transfer, want immediate return")
	}

	close(stub.blockSet)
	waitGroup.Wait()
}

// TestToggleBiasTeeInFlightNoOp asserts a second press while a
// toggle is still running is dropped: only one worker runs, so the
// GPIO is driven exactly once.
func TestToggleBiasTeeInFlightNoOp(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{
		supported:  true,
		blockSet:   make(chan struct{}),
		setEntered: make(chan struct{}),
	}
	adapter, waitGroup, _ := newAdapter(stub)

	// First press: the worker enters SetBiasTee and blocks, holding
	// the in-flight guard.
	adapter.ToggleBiasTee()
	<-stub.setEntered

	// Second press while the first is still in flight: dropped.
	adapter.ToggleBiasTee()

	close(stub.blockSet)
	waitGroup.Wait()

	if setCalls, _ := stub.snapshot(); setCalls != 1 {
		t.Errorf("setCalls = %d, want 1 (second press while in flight must be dropped)", setCalls)
	}
}
