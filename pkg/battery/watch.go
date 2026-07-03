package battery

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const defaultInterval = 30 * time.Second

// ErrUnsupported is returned by a platform reader when the host
// cannot report battery state at all — a GOOS without a battery
// backend, or a machine with no battery present (desktop / CI).
// Watch treats it as terminal: it logs once and stops rather than
// spinning a ticker against a source that will never yield.
var ErrUnsupported = errors.New("battery: not supported on this platform")

// reading is the normalized battery snapshot a platform reader
// returns. percentage is 0..100; charging is the live charge
// state (actively taking on charge, not merely on external power).
type reading struct {
	percentage int8
	charging   bool
}

// reader obtains a single battery reading. Each platform file
// (reader_linux.go / reader_darwin.go / reader_other.go) provides
// newReader(override) so this loop stays platform-agnostic. The
// context lets a slow backend (e.g. a shelled-out pmset) be
// cancelled when the app shuts down mid-read.
type reader func(ctx context.Context) (reading, error)

// Watch updates the battery status periodically, auto-discovering
// the platform's battery source.
func Watch(ctx context.Context, status *Status) error {
	return WatchWithInterval(ctx, status, defaultInterval, "")
}

// WatchWithInterval updates the battery status on a custom
// interval. override is platform-specific: on Linux it is an
// explicit path to a power_supply uevent file (empty =
// auto-discover the first Battery-type device); on macOS it is
// ignored (battery data comes from pmset). Returns nil on ctx
// cancellation.
func WatchWithInterval(ctx context.Context, status *Status, interval time.Duration, override string) error {
	return watchWith(ctx, status, interval, newReader(override))
}

// watchWith is the platform-agnostic poll loop. Split from
// WatchWithInterval so tests can inject a fake reader without
// touching real hardware. Both the initial read and every tick
// treat a transient failure the same way: log a warning and keep
// polling, so a startup udev race doesn't take the app down.
func watchWith(ctx context.Context, status *Status, interval time.Duration, read reader) error {
	if applyOnce(ctx, status, read) {
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if applyOnce(ctx, status, read) {
				return nil
			}
		}
	}
}

// applyOnce performs one read and folds it into status. It
// returns true when the loop should stop: an unsupported source
// is terminal (no point polling), while any other error is a
// transient warning we retry on the next tick.
func applyOnce(ctx context.Context, status *Status, read reader) bool {
	got, err := read(ctx)

	switch {
	case errors.Is(err, ErrUnsupported):
		slog.Info("battery monitoring unavailable on this platform, disabling")

		return true
	case err != nil:
		slog.Warn("battery update failed, will retry", slog.Any("error", err))

		return false
	default:
		status.Update(WithPercentage(got.percentage), WithCharging(got.charging))

		return false
	}
}
