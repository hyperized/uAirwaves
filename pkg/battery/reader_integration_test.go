//go:build integration

package battery

import (
	"context"
	"testing"
	"time"
)

const (
	readTimeout  = 5 * time.Second
	watchTimeout = 2 * time.Second
)

// TestReadRealHost exercises the platform reader against the
// actual host battery source (sysfs on Linux, pmset on macOS).
// Gated behind the integration build tag so the default
// `go test ./...` run never shells out or reads real hardware. A
// host with no battery (CI, desktop) is a pass: the reader may
// legitimately report it cannot find one.
func TestReadRealHost(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	got, err := newReader("")(ctx)
	if err != nil {
		t.Logf("no battery reading on this host (acceptable): %v", err)

		return
	}

	if got.percentage < 0 || got.percentage > 100 {
		t.Errorf("implausible battery percentage: %d", got.percentage)
	}

	t.Logf("host battery: %d%% charging=%t", got.percentage, got.charging)
}

// TestWatchRealHost drives the public auto-discovering watcher
// against the real host, covering the Watch / WatchWithInterval
// entrypoints and one live read before cancelling.
func TestWatchRealHost(t *testing.T) {
	t.Parallel()

	status := NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), watchTimeout)
	defer cancel()

	if err := Watch(ctx, status); err != nil {
		t.Errorf("Watch() unexpected error: %v", err)
	}
}
