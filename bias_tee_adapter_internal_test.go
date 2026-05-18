package main

import (
	"errors"
	"testing"
)

// stubBiasStream implements biasTeeStream for adapter tests.
// Each call increments a counter so the test can assert which
// branch fired; the configurable errors / state drive every
// branch of ToggleBiasTee.
type stubBiasStream struct {
	supported    bool
	enabled      bool
	getErr       error
	setErr       error
	supportCalls int
	getCalls     int
	setCalls     int
	lastSet      bool
}

func (s *stubBiasStream) BiasTeeSupported() bool {
	s.supportCalls++

	return s.supported
}

func (s *stubBiasStream) BiasTeeEnabled() (bool, error) {
	s.getCalls++

	return s.enabled, s.getErr
}

func (s *stubBiasStream) SetBiasTee(enable bool) error {
	s.setCalls++
	s.lastSet = enable

	return s.setErr
}

var (
	errStubBiasGet = errors.New("stub bias-tee get failure")
	errStubBiasSet = errors.New("stub bias-tee set failure")
)

// TestToggleBiasTeeUnsupportedNoOps exercises the early-return
// branch when the active source has no bias-tee surface.
func TestToggleBiasTeeUnsupportedNoOps(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: false}
	adapter := &biasTeeAdapter{stream: stub}

	adapter.ToggleBiasTee()

	if stub.supportCalls != 1 {
		t.Errorf("supportCalls = %d, want 1", stub.supportCalls)
	}

	if stub.getCalls != 0 || stub.setCalls != 0 {
		t.Errorf("get=%d set=%d, want both 0 (unsupported should short-circuit)", stub.getCalls, stub.setCalls)
	}
}

// TestToggleBiasTeeReadFailureNoSet covers the read-error branch:
// BiasTeeEnabled returns an error; the adapter must log + return
// without calling SetBiasTee.
func TestToggleBiasTeeReadFailureNoSet(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: true, getErr: errStubBiasGet}
	adapter := &biasTeeAdapter{stream: stub}

	adapter.ToggleBiasTee()

	if stub.getCalls != 1 {
		t.Errorf("getCalls = %d, want 1", stub.getCalls)
	}

	if stub.setCalls != 0 {
		t.Errorf("setCalls = %d, want 0 (read failure should skip Set)", stub.setCalls)
	}
}

// TestToggleBiasTeeSetFailureLogged covers the set-error branch.
// The adapter still calls Set; the failure is logged and the call
// returns.
func TestToggleBiasTeeSetFailureLogged(t *testing.T) {
	t.Parallel()

	stub := &stubBiasStream{supported: true, enabled: false, setErr: errStubBiasSet}
	adapter := &biasTeeAdapter{stream: stub}

	adapter.ToggleBiasTee()

	if stub.setCalls != 1 || !stub.lastSet {
		t.Errorf("set=(%d, %v), want (1, true) — off → on toggle still attempted", stub.setCalls, stub.lastSet)
	}
}

// TestToggleBiasTeeSuccessFlipsBit covers the happy path: read
// returns enabled=false, adapter calls SetBiasTee(true).
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
			adapter := &biasTeeAdapter{stream: stub}

			adapter.ToggleBiasTee()

			if stub.setCalls != 1 {
				t.Fatalf("setCalls = %d, want 1", stub.setCalls)
			}

			if stub.lastSet != testCase.wantSet {
				t.Errorf("lastSet = %v, want %v", stub.lastSet, testCase.wantSet)
			}
		})
	}
}
