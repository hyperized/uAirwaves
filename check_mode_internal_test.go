package main

import (
	"testing"
	"time"
)

// checkExitBadInput mirrors runCheckMode's internal bad-input exit code
// so the assertions read as specifications.
const checkExitBadInput = 2

// TestRunCheckModeRejectsOutOfRangeDuration covers the duration
// validation guard: both a sub-second and an over-five-minute window are
// rejected before any worker starts, returning the bad-input code.
func TestRunCheckModeRejectsOutOfRangeDuration(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		duration time.Duration
	}{
		{"below the one-second floor", 500 * time.Millisecond},
		{"above the five-minute ceiling", 6 * time.Minute},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := runCheckMode(cliConfig{checkDuration: testCase.duration}); got != checkExitBadInput {
				t.Errorf("runCheckMode = %d, want %d", got, checkExitBadInput)
			}
		})
	}
}

// TestRunCheckModeReportsWorkerError covers the runErr branch: a valid
// duration passes validation and the workers start, but the ADSB source
// (a replay of a non-existent file) fails its first open. That error
// propagates out of check.Run, so runCheckMode returns the bad-input
// code. The gpsd address is a dead port so the GPS worker never blocks
// on a real socket. check.Run writes its JSON report to stdout by design.
func TestRunCheckModeReportsWorkerError(t *testing.T) {
	t.Parallel()

	cfg := cliConfig{
		checkDuration: 1 * time.Second,
		replayIQPath:  "/nonexistent/uairwaves-check.iq",
		gpsdAddress:   "127.0.0.1:0",
	}

	if got := runCheckMode(cfg); got != checkExitBadInput {
		t.Errorf("runCheckMode = %d, want %d (worker error)", got, checkExitBadInput)
	}
}
