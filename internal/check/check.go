package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
	"github.com/hyperized/uAirwaves/pkg/location"
)

var (
	errGPSRecover         = errors.New("recovered in check gps goroutine")
	errADSBRecover        = errors.New("recovered in check adsb goroutine")
	errDurationBelowMin   = errors.New("check duration below minimum")
	errDurationAboveMax   = errors.New("check duration above maximum")
	errParseCheckDuration = errors.New("parse check duration")
)

// Options configures a check run.
type Options struct {
	Duration   time.Duration
	Thresholds Thresholds
	Output     io.Writer
	Stream     *adsb.ADSB
	Planes     *airplanes.Airplanes
	Location   *location.Location
	StartGPS   func(context.Context) error
	StartADSB  func(context.Context) error
}

// Run drives the GPS and ADSB workers for opts.Duration, gathers
// final snapshots, writes the JSON report to opts.Output, and
// returns it. The bool indicates whether the configured
// thresholds were met (mirrors report.OK).
//
// Run respects parent cancellation: a cancelled parent triggers
// early shutdown and a report describing whatever was observed up
// to that point.
func Run(parent context.Context, opts Options) (Report, bool, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	waitGroup := &sync.WaitGroup{}
	errChan := make(chan error, 2) //nolint:mnd // one slot per worker.

	launch(waitGroup, errChan, errGPSRecover, func() error {
		return opts.StartGPS(ctx)
	})
	launch(waitGroup, errChan, errADSBRecover, func() error {
		return opts.StartADSB(ctx)
	})

	var runErr error

	select {
	case <-parent.Done():
		runErr = parent.Err()
	case <-time.After(opts.Duration):
	case err := <-errChan:
		runErr = err
	}

	// Sample Source() before cancelling so the report reflects the
	// connection state at end-of-window, not after the deferred
	// connected.Store(false) inside the workers has already fired.
	sourceAtEnd := opts.Stream.Source()

	cancel()
	waitGroup.Wait()

	report := buildFromLive(opts, sourceAtEnd)

	if err := writeJSON(opts.Output, report); err != nil {
		return report, report.OK, err
	}

	return report, report.OK, runErr
}

// launch mirrors internal/ui.LaunchWorker but stays inside the
// check package to avoid a cycle with the UI helpers — check is
// a UI-less mode by design.
func launch(waitGroup *sync.WaitGroup, errChan chan<- error, panicSentinel error, worker func() error) {
	waitGroup.Go(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok {
					errChan <- errors.Join(err, panicSentinel)
				}
			}
		}()

		if err := worker(); err != nil {
			errChan <- err
		}
	})
}

func buildFromLive(opts Options, source adsb.SourceInfo) Report {
	lat, lon := opts.Location.GetCoordinates()
	snapshots := opts.Planes.Sorted(lat, lon)

	return BuildReport(Inputs{
		DurationSeconds: opts.Duration.Seconds(),
		Stats:           opts.Stream.Stats(),
		Source:          source,
		Snapshots:       snapshots,
		ReceiverLat:     lat,
		ReceiverLon:     lon,
		GPSFix:          opts.Location.HasFix(),
		GPSMode:         opts.Location.Mode(),
		GPSAltitudeM:    opts.Location.Altitude(),
		Thresholds:      opts.Thresholds,
	})
}

func writeJSON(out io.Writer, report Report) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode check report: %w", err)
	}

	return nil
}

// ParseDuration parses a Go duration string and rejects values
// below 1 second or above 5 minutes. The bounds keep operators
// from accidentally requesting useless windows (sub-second) or
// hanging a CI job indefinitely.
func ParseDuration(raw string) (time.Duration, error) {
	dur, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w %q: %w", errParseCheckDuration, raw, err)
	}

	return ValidateDuration(dur)
}

// ValidateDuration enforces the same 1 s – 5 min bounds as
// ParseDuration, but on an already-parsed value. Used when the
// caller has already obtained a time.Duration (e.g. from the
// flag.DurationVar binding) and just needs the range check.
func ValidateDuration(dur time.Duration) (time.Duration, error) {
	const (
		minDuration = 1 * time.Second
		maxDuration = 5 * time.Minute
	)

	if dur < minDuration {
		return 0, fmt.Errorf("%w: %s < %s", errDurationBelowMin, dur, minDuration)
	}

	if dur > maxDuration {
		return 0, fmt.Errorf("%w: %s > %s", errDurationAboveMax, dur, maxDuration)
	}

	return dur, nil
}

// LogStart writes a single human-readable line to stderr noting
// the upcoming check window. Useful when stdout is being piped to
// jq and the operator wants a visible "starting" marker.
func LogStart(duration time.Duration) {
	slog.Info("check mode starting", slog.Duration("duration", duration))
}
