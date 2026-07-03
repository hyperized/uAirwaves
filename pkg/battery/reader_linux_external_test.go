//go:build linux

package battery_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/battery"
)

const (
	watchTimeout  = 200 * time.Millisecond
	watchInterval = 10 * time.Millisecond
	settle        = 50 * time.Millisecond
)

// TestWatchWithInterval_TempUevent drives the public watcher
// against an explicit uevent path (the --battery override), then
// rewrites the file and confirms the next tick picks up the new
// value. Hermetic: a temp file, no real power_supply device.
func TestWatchWithInterval_TempUevent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "uevent")
	if err := os.WriteFile(path, []byte("POWER_SUPPLY_STATUS=Discharging\nPOWER_SUPPLY_CAPACITY=50\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), watchTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- battery.WatchWithInterval(ctx, status, watchInterval, path) }()

	if !waitForPercent(t, status, 50) {
		t.Fatalf("expected 50, got %d", status.GetPercentage())
	}

	if err := os.WriteFile(path, []byte("POWER_SUPPLY_STATUS=Charging\nPOWER_SUPPLY_CAPACITY=60\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !waitForPercent(t, status, 60) {
		t.Fatalf("expected 60 after rewrite, got %d", status.GetPercentage())
	}

	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("WatchWithInterval() unexpected error: %v", err)
	}
}

// TestWatchWithInterval_MissingPath verifies an unreadable path is
// logged and retried rather than fatal, so a startup udev race
// doesn't take the app down.
func TestWatchWithInterval_MissingPath(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), settle)
	defer cancel()

	if err := battery.WatchWithInterval(ctx, status, watchInterval, "/non/existent/uevent"); err != nil {
		t.Errorf("expected nil on ctx cancel, got %v", err)
	}

	if status.GetPercentage() != 0 {
		t.Errorf("expected percentage to stay 0, got %d", status.GetPercentage())
	}
}

// TestWatch_Discovers exercises the auto-discovery entrypoint
// against the host's real /sys/class/power_supply. A pseudo-fs
// read (no external service): whether or not the host has a
// battery, Watch must return nil on ctx cancel.
func TestWatch_Discovers(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), settle)
	defer cancel()

	if err := battery.Watch(ctx, status); err != nil {
		t.Errorf("Watch() unexpected error: %v", err)
	}
}

// waitForPercent polls status until it reports want or the timeout
// elapses.
func waitForPercent(t *testing.T, status *battery.Status, want int8) bool {
	t.Helper()

	deadline := time.Now().Add(watchTimeout)
	for time.Now().Before(deadline) {
		if status.GetPercentage() == want {
			return true
		}

		time.Sleep(time.Millisecond)
	}

	return status.GetPercentage() == want
}
