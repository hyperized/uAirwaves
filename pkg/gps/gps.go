// Package gps connects to a gpsd daemon and feeds TPV position
// reports into a shared location. It reconnects with exponential
// backoff on dial failures, server hangups, and stalled sockets, and
// can fire a callback on every report that carries a real fix so
// callers can gate a GPS-freshness fallback.
package gps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/stratoberry/go-gpsd"
)

var (
	errDial = errors.New("failed to dial gpsd service")
	// errSessionClosed is returned by watchOnce when gpsd's
	// `done` channel fires without ctx being cancelled — i.e.
	// the server hung up on us. Watch treats it as a reconnect
	// trigger so the outer loop does not silently exit on a
	// remote disconnect.
	errSessionClosed = errors.New("gpsd session closed by server")
	// errTPVTimeout is returned by watchOnce when the watchdog
	// notices no TPV report has been delivered for tpvTimeout.
	// gpsd normally streams TPV at 1 Hz regardless of fix state,
	// so a 30 s gap means either gpsd died quietly or the TCP
	// socket stalled (no FIN, no data). Both call for a fresh
	// connect, which the outer reconnect loop handles.
	errTPVTimeout = errors.New("no TPV report received within watchdog window")
)

// Session defines the interface for a gpsd session.
type Session interface {
	AddFilter(filterType string, filter gpsd.Filter)
	Watch() chan bool
	Close() error
}

// FixCallback is invoked on every TPV report that carries a
// non-zero lat/lon. The time argument is the wall-clock instant
// the report was delivered. Used by the self-locate coordinator
// to decide when GPS is "fresh" vs. when the ADSB-derived
// fallback should fill in.
type FixCallback func(time.Time)

// GPS represents a GPS daemon connection.
type GPS struct {
	gpsAddress, serviceName, protocol string
	session                           Session
	dial                              func(string) (Session, error)
	reconnect                         bool
	onFix                             FixCallback
}

// Option defines the function signature for configuring a GPS instance.
type Option func(*GPS)

// New creates a new GPS instance with default values and applies provided options.
func New(opts ...Option) *GPS {
	gps := &GPS{
		gpsAddress:  "127.0.0.1:2947",
		serviceName: "gpsd.service",
		protocol:    "tcp",
		dial: func(address string) (Session, error) {
			s, err := gpsd.Dial(address)
			if err != nil {
				return nil, fmt.Errorf("failed to dial gpsd: %w", err)
			}

			return s, nil
		},
	}

	for _, opt := range opts {
		opt(gps)
	}

	return gps
}

// WithGpsAddress sets the address for the gpsd connection.
func WithGpsAddress(address string) Option {
	return func(g *GPS) {
		g.gpsAddress = address
	}
}

// WithServiceName sets the systemd service name to manage.
func WithServiceName(name string) Option {
	return func(g *GPS) {
		g.serviceName = name
	}
}

// WithProtocol sets the protocol to use for the gpsd connection.
func WithProtocol(protocol string) Option {
	return func(g *GPS) {
		g.protocol = protocol
	}
}

// WithReconnect enables automatic reconnection with exponential backoff on errors.
func WithReconnect(reconnect bool) Option {
	return func(g *GPS) {
		g.reconnect = reconnect
	}
}

// WithFixCallback registers a callback that fires on every TPV
// with valid lat/lon. nil disables the hook. The callback is
// invoked from the gpsd-handler goroutine, so it must be cheap
// and lock-friendly; a typical implementation just stamps an
// atomic time so the self-locate coordinator can gate its
// fallback on GPS freshness.
func WithFixCallback(fn FixCallback) Option {
	return func(g *GPS) {
		g.onFix = fn
	}
}

const (
	reconnectBaseDelay = 1 * time.Second
	reconnectMaxDelay  = 30 * time.Second
	// tpvTimeout is the watchdog window. gpsd streams TPV at 1 Hz
	// even when the receiver has no fix (mode=1), so a 30 s gap
	// is well outside normal operation and almost certainly a
	// stalled connection. The watchdog returns errTPVTimeout to
	// force a reconnect; without it the outer loop would block
	// forever on a silently-broken socket. 30 s also matches the
	// reconnect backoff cap so a flapping link doesn't busy-loop.
	tpvTimeout = 30 * time.Second
	// tpvWatchdogTick is how often the watchdog checks the
	// last-TPV timestamp. A 1 Hz tick is well below tpvTimeout
	// so the timeout fires within ~1 s of the gap exceeding the
	// threshold.
	tpvWatchdogTick = 1 * time.Second
	// noTPVYet is the prevMode sentinel for "no TPV report has
	// arrived in this watch cycle". Real gpsd modes start at 0,
	// so a negative value cannot collide with one.
	noTPVYet int32 = -1
)

// Watch starts the GPS monitoring process.
// When reconnect is enabled, it retries with exponential backoff on errors,
// including the case where gpsd hangs up server-side (errSessionClosed) and
// the case where the connection is silently stalled (errTPVTimeout).
// Without those distinctions, a remote disconnect looked identical to ctx
// cancellation and the outer loop exited silently.
//
// Repeated failures go through a reconnectLogger, so a machine with no
// gpsd reports the problem once instead of once per backoff window.
func (g *GPS) Watch(ctx context.Context, myLocation *location.Location) error {
	var (
		backoff  = reconnectBaseDelay
		failures reconnectLogger
	)

	for {
		delivered, err := g.watchOnce(ctx, myLocation)
		if err == nil {
			return nil
		}

		if !g.reconnect {
			return err
		}

		if delivered {
			failures.delivered()
		}

		failures.report(err, backoff)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
			backoff = min(backoff*2, reconnectMaxDelay)
		}
	}
}

// reconnectLogger holds Watch's reconnect warnings down to the ones that
// tell the operator something new. A missing gpsd fails the same way on
// every attempt, and in TUI mode each Warn queues another notification to
// dismiss, all of it repeating what the header already says (position
// switched to the inferred self-locate estimate). So the first failure of
// a kind warns, identical repeats drop to a counted Debug line, and only
// a cycle that carried TPV data lets the next failure warn again.
//
// Owned by the single Watch loop; not safe for concurrent use.
type reconnectLogger struct {
	last    string
	repeats int
	warned  bool
}

// delivered clears the de-duplication state after a watch cycle that saw
// at least one TPV report: gpsd was alive, so whatever broke afterwards
// is a new problem rather than the old one repeating.
func (r *reconnectLogger) delivered() {
	r.warned = false
}

// report logs one failed watch cycle: a Warn for a failure that has not
// been reported yet, a counted Debug line while the same one repeats.
func (r *reconnectLogger) report(err error, delay time.Duration) {
	if r.warned && err.Error() == r.last {
		r.repeats++

		slog.Debug("GPS still unavailable, reconnecting",
			slog.Any("error", err),
			slog.Int("repeats", r.repeats),
			slog.Duration("delay", delay),
		)

		return
	}

	slog.Warn("GPS watch error, reconnecting", slog.Any("error", err), slog.Duration("delay", delay))

	r.last = err.Error()
	r.repeats = 0
	r.warned = true
}

// watchOnce runs one connect-and-watch cycle. It reports whether gpsd
// delivered at least one TPV report during the cycle, which is how Watch
// tells a fresh failure from a repeat of one it already reported.
func (g *GPS) watchOnce(ctx context.Context, myLocation *location.Location) (bool, error) {
	if err := g.connect(); err != nil {
		return false, err
	}

	defer g.disconnect()

	// lastTPV stores time.Now().UnixNano() of the most recent
	// TPV delivery so the watchdog can spot a stalled socket.
	// Initialised to "now" so the first watchdog tick doesn't
	// fire immediately on a freshly-opened session.
	var lastTPV atomic.Int64
	lastTPV.Store(time.Now().UnixNano())

	// prevMode tracks the last delivered TPV mode so we can
	// emit a one-shot info-level log on every transition —
	// "GPS mode change from=1 to=3" tells the operator the
	// receiver acquired a fix even if the rest of the UI is
	// idle. noTPVYet marks the pre-first-TPV sentinel.
	var prevMode atomic.Int32
	prevMode.Store(noTPVYet)

	g.session.AddFilter("TPV", buildTPVHandler(myLocation, &lastTPV, &prevMode, g.onFix))

	done := g.session.Watch()

	err := waitForSession(ctx, done, &lastTPV, tpvWatchdogTick, tpvTimeout)

	// prevMode leaves the noTPVYet sentinel behind on the first report,
	// so it doubles as "did this cycle hear anything at all".
	return prevMode.Load() != noTPVYet, err
}

// buildTPVHandler returns the gpsd Filter callback bound to the
// shared lastTPV / prevMode watchdog state. Pulled out of
// watchOnce so the closure body stays inside revive's
// cognitive-complexity gate.
func buildTPVHandler(
	myLocation *location.Location, lastTPV *atomic.Int64, prevMode *atomic.Int32, onFix FixCallback,
) gpsd.Filter {
	return func(t any) {
		tpvReport, ok := t.(*gpsd.TPVReport)
		if !ok {
			return
		}

		lastTPV.Store(time.Now().UnixNano())

		newMode := int32(tpvReport.Mode)
		if oldMode := prevMode.Swap(newMode); oldMode != newMode && oldMode != noTPVYet {
			slog.Info("GPS mode change",
				slog.Int("from", int(oldMode)),
				slog.Int("to", int(newMode)),
			)
		}

		myLocation.Update(
			location.WithSource(location.SourceGPS),
			// Clear any stale self-locate figures: once gpsd speaks,
			// this position is a GPS fix, not an inferred estimate.
			location.WithConfidenceRadiusNm(0),
			location.WithSpreadNm(0),
			location.WithMode(int(tpvReport.Mode)),
			location.WithLatitude(tpvReport.Lat),
			location.WithLongitude(tpvReport.Lon),
			location.WithAltitude(tpvReport.Alt),
		)

		// Fire the freshness hook only when the TPV carries a
		// real position. Mode 1 (no fix) and zero lat/lon mean
		// gpsd is alive but the receiver hasn't locked yet —
		// self-locate must still be allowed to fill in.
		if onFix != nil && (tpvReport.Lat != 0 || tpvReport.Lon != 0) {
			onFix(time.Now())
		}
	}
}

// waitForSession blocks until the session's done channel fires,
// the context is cancelled, or the watchdog notices a stalled
// connection. tick controls how often the watchdog polls; timeout
// is the maximum gap between TPV reports before the watchdog
// forces a reconnect. Pulled out (and parameterised) so watchOnce
// stays small and so internal tests can drive a fast watchdog.
func waitForSession(
	ctx context.Context, done <-chan bool, lastTPV *atomic.Int64, tick, timeout time.Duration,
) error {
	watchdog := time.NewTicker(tick)
	defer watchdog.Stop()

	for {
		select {
		case <-done:
			// gpsd's done channel fired without ctx being cancelled —
			// the server hung up. Surface a sentinel so Watch can
			// distinguish this from a clean shutdown and reconnect.
			return errSessionClosed
		case <-ctx.Done():
			return nil
		case <-watchdog.C:
			elapsed := time.Since(time.Unix(0, lastTPV.Load()))
			if elapsed > timeout {
				return errTPVTimeout
			}
		}
	}
}

func (g *GPS) connect() error {
	var err error

	g.session, err = g.dial(g.gpsAddress)
	if err != nil {
		return errors.Join(err, errDial)
	}

	return nil
}

func (g *GPS) disconnect() {
	if err := g.session.Close(); err != nil {
		slog.Warn("Disconnecting from GPS daemon", slog.Any("error", err))

		return
	}

	slog.Info("Disconnecting from GPS daemon")
}
