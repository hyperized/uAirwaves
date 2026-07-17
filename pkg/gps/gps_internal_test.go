package gps

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/stratoberry/go-gpsd"
)

type mockSession struct {
	mu       sync.RWMutex
	filters  map[string]gpsd.Filter
	done     chan bool
	closed   bool
	closeErr error
}

func (m *mockSession) AddFilter(f string, filter gpsd.Filter) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.filters[f] = filter
}

func (m *mockSession) Watch() chan bool {
	return m.done
}

func (m *mockSession) Close() error {
	m.closed = true

	return m.closeErr
}

func (m *mockSession) getFilter(f string) (gpsd.Filter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter, found := m.filters[f]

	return filter, found
}

func TestWatch(t *testing.T) {
	t.Parallel()

	testSuccessfulWatch(t)
	testSessionDone(t)
	testSessionDoneReconnects(t)
	testConnectError(t)
	testInvalidReportType(t)
}

var (
	errDialFailed = errors.New("dial failed")
	errCloseFake  = errors.New("fake close failure")
)

// TestDisconnectBranches exercises both arms of disconnect() so
// coverage covers the Warn-on-error and Info-on-clean paths; the
// log output itself is not asserted (slog default handler), only
// that the right Close return value drives the branching.
func TestDisconnectBranches(t *testing.T) {
	t.Parallel()

	t.Run("clean close logs at Info, no error key", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{filters: map[string]gpsd.Filter{}, done: make(chan bool)}
		gpsInstance := New(func(gps *GPS) { gps.session = session })

		gpsInstance.disconnect()

		if !session.closed {
			t.Error("Close was not called on clean disconnect")
		}
	})

	t.Run("close error logs at Warn", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters:  map[string]gpsd.Filter{},
			done:     make(chan bool),
			closeErr: errCloseFake,
		}
		gpsInstance := New(func(gps *GPS) { gps.session = session })

		gpsInstance.disconnect()

		if !session.closed {
			t.Error("Close was not called on error disconnect")
		}
	})
}

func testSuccessfulWatch(t *testing.T) {
	t.Helper()
	t.Run("successful watch", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		myLoc := location.New()
		ctx, cancel := context.WithCancel(t.Context())

		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(ctx, myLoc)
		}()

		// Give it a moment to connect and add filters
		time.Sleep(10 * time.Millisecond)

		if filter, found := session.getFilter("TPV"); found {
			filter(&gpsd.TPVReport{
				Mode: 3,
				Lat:  52.5,
				Lon:  13.4,
				Alt:  100.5,
			})
		} else {
			t.Fatal("TPV filter not added")
		}

		lat, lon := myLoc.GetCoordinates()
		if lat != 52.5 || lon != 13.4 {
			t.Errorf("expected coordinates (52.5, 13.4), got (%f, %f)", lat, lon)
		}

		cancel()

		err := <-errCh
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if !session.closed {
			t.Error("expected session to be closed")
		}
	})
}

func testSessionDone(t *testing.T) {
	t.Helper()
	t.Run("session done without reconnect surfaces errSessionClosed", func(t *testing.T) {
		t.Parallel()

		// Without reconnect, a server-side hangup must propagate
		// as an error so the caller can decide what to do. Pre-C3
		// this path silently returned nil and the outer worker
		// loop in main.go exited without notice.
		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(t.Context(), location.New())
		}()

		time.Sleep(10 * time.Millisecond)
		close(session.done)

		err := <-errCh
		if !errors.Is(err, errSessionClosed) {
			t.Errorf("expected errSessionClosed, got %v", err)
		}
	})

	t.Run("ctx cancellation returns nil", func(t *testing.T) {
		t.Parallel()

		// Ctx-driven exit must still be a clean nil return — the
		// caller is asking us to stop, not telling us we failed.
		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(ctx, location.New())
		}()

		time.Sleep(10 * time.Millisecond)
		cancel()

		err := <-errCh
		if err != nil {
			t.Errorf("ctx cancel should return nil, got %v", err)
		}
	})
}

// testSessionDoneReconnects locks in the C3 fix: with reconnect
// enabled, a server-side hangup must drive the outer Watch loop
// into a fresh watchOnce instead of silently exiting. We let the
// first session close, observe a second dial happen, then cancel
// ctx to end the test cleanly.
func testSessionDoneReconnects(t *testing.T) {
	t.Helper()
	t.Run("reconnects on server hangup", func(t *testing.T) {
		t.Parallel()

		// Two pre-built sessions; each Dial pops the next.
		first := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}
		second := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		var (
			dialMu    sync.Mutex
			dialCount int
		)

		gpsInstance := New(
			WithReconnect(true),
			func(gps *GPS) {
				gps.dial = func(_ string) (Session, error) {
					dialMu.Lock()
					defer dialMu.Unlock()

					dialCount++
					if dialCount == 1 {
						return first, nil
					}

					return second, nil
				}
			},
		)

		// Shrink the reconnect delay for the test — we rely on
		// the default base of 1s being a problem if we don't.
		// The base is a package const so we use a tight poll
		// loop instead and accept a one-second backoff.

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(ctx, location.New())
		}()

		// Wait for first dial to land, then close its done
		// channel to simulate the server hanging up.
		waitFor(t, func() bool {
			dialMu.Lock()
			defer dialMu.Unlock()

			return dialCount >= 1
		})
		close(first.done)

		// Reconnect must redial. Backoff starts at 1s so this
		// poll waits up to ~3s for the second dial.
		waitForUpTo(t, 3*time.Second, func() bool {
			dialMu.Lock()
			defer dialMu.Unlock()

			return dialCount >= 2
		})

		dialMu.Lock()
		got := dialCount
		dialMu.Unlock()

		if got < 2 {
			t.Fatalf("expected reconnect to redial; dialCount=%d", got)
		}

		cancel()

		err := <-errCh
		if err != nil {
			t.Errorf("ctx-cancelled Watch should return nil, got %v", err)
		}
	})
}

// waitFor blocks until pred returns true, polling every 5ms with
// a default 500ms deadline. Fails the test if the deadline lapses.
func waitFor(t *testing.T, pred func() bool) {
	t.Helper()
	waitForUpTo(t, 500*time.Millisecond, pred)
}

func waitForUpTo(t *testing.T, deadline time.Duration, pred func() bool) {
	t.Helper()

	stopAt := time.Now().Add(deadline)
	for time.Now().Before(stopAt) {
		if pred() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("predicate never became true within %s", deadline)
}

func testConnectError(t *testing.T) {
	t.Helper()
	t.Run("connect error", func(t *testing.T) {
		t.Parallel()

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return nil, errDialFailed
			}
		})

		err := gpsInstance.Watch(t.Context(), location.New())
		if !errors.Is(err, errDial) {
			t.Errorf("expected errDial, got %v", err)
		}
	})
}

func testInvalidReportType(t *testing.T) {
	t.Helper()
	t.Run("invalid report type", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		myLoc := location.New()

		go func() {
			_ = gpsInstance.Watch(t.Context(), myLoc)
		}()

		time.Sleep(10 * time.Millisecond)

		if filter, found := session.getFilter("TPV"); found {
			filter("not a TPV report") // Should not panic
		}
	})
}

// TestBuildTPVHandlerModeChangeAndFix drives the handler closure
// across two TPV reports to reach the branches a single-report Watch
// never touches: the mode-transition log (needs a real from→to where
// the previous mode is not the -1 sentinel) and the fix callback
// (needs a non-nil onFix plus a report carrying a real position). The
// first report seeds prevMode from the sentinel and carries no
// position, so it must neither log a transition nor fire the callback;
// the second flips the mode and carries a fix, so it must do both.
func TestBuildTPVHandlerModeChangeAndFix(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32

	gpsInstance := New(WithFixCallback(func(time.Time) { fired.Add(1) }))
	if gpsInstance.onFix == nil {
		t.Fatal("WithFixCallback did not set onFix")
	}

	var (
		lastTPV  atomic.Int64
		prevMode atomic.Int32
	)

	prevMode.Store(-1)

	myLoc := location.New()
	handler := buildTPVHandler(myLoc, &lastTPV, &prevMode, gpsInstance.onFix)

	// First report: mode 1 (no fix), no position. Seeds prevMode
	// off the -1 sentinel (so no transition log) and must leave the
	// callback untouched because lat/lon are zero.
	handler(&gpsd.TPVReport{Mode: 1, Lat: 0, Lon: 0})

	if got := fired.Load(); got != 0 {
		t.Errorf("onFix fired %d times on zero-position report, want 0", got)
	}

	// Second report: mode flips 1→3 and carries a real position, so
	// both the transition log and the callback must run.
	handler(&gpsd.TPVReport{Mode: 3, Lat: 52.5, Lon: 13.4})

	if got := fired.Load(); got != 1 {
		t.Errorf("onFix fired %d times after positioned fix, want 1", got)
	}

	if lat, lon := myLoc.GetCoordinates(); lat != 52.5 || lon != 13.4 {
		t.Errorf("location = (%f, %f), want (52.5, 13.4)", lat, lon)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	gpsInstance := New()
	if gpsInstance.gpsAddress != "127.0.0.1:2947" {
		t.Errorf("expected default gpsAddress 127.0.0.1:2947, got %s", gpsInstance.gpsAddress)
	}

	if gpsInstance.dial == nil {
		t.Error("expected default dial function to be set")
	}

	// Test default dial function error path (requires no gpsd running on default port)
	_, err := gpsInstance.dial("127.0.0.1:0")
	if err == nil {
		t.Error("expected error dialing invalid address")
	}

	t.Run("default dial success", func(t *testing.T) {
		t.Parallel()

		var lc net.ListenConfig

		listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, err := listener.Accept()
			if err == nil {
				_ = conn.Close()
			}
		}()

		session, err := gpsInstance.dial(listener.Addr().String())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if session != nil {
			_ = session.Close()
		}
	})
}

func TestOptions(t *testing.T) {
	t.Parallel()

	gpsInstance := New(
		WithGpsAddress("1.2.3.4:5678"),
		WithServiceName("custom.service"),
		WithProtocol("udp"),
	)

	if gpsInstance.gpsAddress != "1.2.3.4:5678" {
		t.Errorf("expected gpsAddress 1.2.3.4:5678, got %s", gpsInstance.gpsAddress)
	}

	if gpsInstance.serviceName != "custom.service" {
		t.Errorf("expected serviceName custom.service, got %s", gpsInstance.serviceName)
	}

	if gpsInstance.protocol != "udp" {
		t.Errorf("expected protocol udp, got %s", gpsInstance.protocol)
	}
}

// TestWithReconnectTogglesField locks the WithReconnect contract:
// the option flips the private reconnect field used by Watch's
// outer loop to decide between "redial on error" and "return the
// error to the caller". The field is not exported; this internal
// test is the only place that can read it.
func TestWithReconnectTogglesField(t *testing.T) {
	t.Parallel()

	defaultGPS := New()
	if defaultGPS.reconnect {
		t.Error("default reconnect should be false")
	}

	enabledGPS := New(WithReconnect(true))
	if !enabledGPS.reconnect {
		t.Error("WithReconnect(true) did not set the field")
	}

	disabledGPS := New(WithReconnect(true), WithReconnect(false))
	if disabledGPS.reconnect {
		t.Error("WithReconnect(false) did not clear the field after enable")
	}
}

// TestWatchCtxCancelDuringBackoff drives the backoff arm of
// Watch's select: once watchOnce returns errSessionClosed and the
// outer loop enters time.After(backoff), cancelling ctx must
// short-circuit the sleep and produce a nil return (graceful
// exit). Without that branch a ctx cancel during backoff would
// block for up to reconnectMaxDelay before the loop noticed.
func TestWatchCtxCancelDuringBackoff(t *testing.T) {
	t.Parallel()

	session := &mockSession{
		filters: make(map[string]gpsd.Filter),
		done:    make(chan bool),
	}

	gpsInstance := New(
		WithReconnect(true),
		func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		},
	)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)

	go func() {
		errCh <- gpsInstance.Watch(ctx, location.New())
	}()

	// Wait for Watch to enter the session select, then close the
	// session so watchOnce returns errSessionClosed and the outer
	// loop drops into time.After(backoff). The base delay is 1s
	// — plenty of time to fire the cancel before the sleep
	// elapses.
	time.Sleep(20 * time.Millisecond) //nolint:mnd // long enough for the goroutine to enter watchOnce.
	close(session.done)

	// Briefly let the loop transition into backoff, then cancel.
	time.Sleep(20 * time.Millisecond) //nolint:mnd // bound for the loop to reach select.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("ctx-cancel during backoff should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second): //nolint:mnd // would-fail-anyway deadline; passes well under 100ms in practice.
		t.Fatal("Watch did not return after ctx cancel during backoff")
	}
}

// TestWaitForSessionWatchdogFiresOnStall pins the contract that
// waitForSession returns errTPVTimeout when no TPV has been seen
// within the configured window — the defensive path against a
// silently-stalled gpsd socket where Done would never fire.
func TestWaitForSessionWatchdogFiresOnStall(t *testing.T) {
	t.Parallel()

	var lastTPV atomic.Int64
	// Park lastTPV well in the past so the watchdog sees the
	// gap on the very next tick.
	lastTPV.Store(time.Now().Add(-1 * time.Second).UnixNano())

	done := make(chan bool)

	const (
		tick    = 5 * time.Millisecond
		timeout = 10 * time.Millisecond
	)

	err := waitForSession(t.Context(), done, &lastTPV, tick, timeout)
	if !errors.Is(err, errTPVTimeout) {
		t.Errorf("expected errTPVTimeout when no TPV in window, got %v", err)
	}
}

// TestWaitForSessionTPVKeepsAlive pins the converse: a TPV
// callback that bumps lastTPV inside the window keeps the
// watchdog quiet. We simulate the callback by writing the
// timestamp ourselves at half the timeout interval.
func TestWaitForSessionTPVKeepsAlive(t *testing.T) {
	t.Parallel()

	var lastTPV atomic.Int64
	lastTPV.Store(time.Now().UnixNano())

	done := make(chan bool)

	const (
		tick      = 5 * time.Millisecond
		timeout   = 50 * time.Millisecond
		runFor    = 120 * time.Millisecond
		bumpEvery = 20 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(t.Context(), runFor)
	defer cancel()

	go func() {
		ticker := time.NewTicker(bumpEvery)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				lastTPV.Store(time.Now().UnixNano())
			}
		}
	}()

	err := waitForSession(ctx, done, &lastTPV, tick, timeout)
	if err != nil {
		t.Errorf("watchdog should stay quiet while TPVs arrive, got %v", err)
	}
}
