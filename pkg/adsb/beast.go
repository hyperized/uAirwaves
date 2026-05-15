package adsb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/hyperized/demod1090/beast"
	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

// countingReader is a thin io.Reader wrapper that bumps an
// atomic.Uint64 by the byte count of every successful Read. Used
// on the BEAST conn so the UI can surface "bytes pulled from the
// remote source" in the header without reaching into beast.Reader.
type countingReader struct {
	inner   io.Reader
	counter *atomic.Uint64
}

func (c *countingReader) Read(buf []byte) (int, error) {
	//nolint:varnamelen // io.Reader contract uses (n, err); the idiom is universal.
	n, err := c.inner.Read(buf)
	if n > 0 {
		c.counter.Add(uint64(n))
	}

	//nolint:wrapcheck // pass-through wrapper: the caller already wraps stream errors.
	return n, err
}

// BeastDialer is the seam Stream uses to reach a BEAST server.
// net.Dialer satisfies it via DialContext; tests inject a
// loopback-only dialer that bypasses the system resolver.
type BeastDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// defaultBeastDialer is the production dialer: a zero-value
// net.Dialer with no timeout (DialContext honours ctx instead).
//
//nolint:gochecknoglobals // production default for the BeastDialer seam.
var defaultBeastDialer BeastDialer = &net.Dialer{}

// errBeastServerHangup signals that the BEAST stream ended
// cleanly (io.EOF). The outer reconnect loop maps it to a backoff
// retry rather than a hard error — the server may have restarted
// and a fresh dial will get us back into the fan-out.
var errBeastServerHangup = errors.New("adsb: beast server hung up")

// streamBeast keeps a BEAST consumer alive across reconnects. It
// returns nil only on ctx cancellation; every connect / read
// error is logged and retried with exponential backoff (1 s base,
// 30 s cap, doubling). Mirrors pkg/gps.Watch's WithReconnect=true
// path so a single shape covers both TCP-backed sources.
func (a *ADSB) streamBeast(ctx context.Context, planes *airplanes.Airplanes) error {
	backoff := beastReconnectBaseDelay

	for {
		err := a.streamBeastOnce(ctx, planes)
		if err == nil {
			return nil
		}

		slog.Warn("adsb: beast stream error, reconnecting",
			slog.String("address", a.beastAddress),
			slog.Any("error", err),
			slog.Duration("delay", backoff),
		)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
			backoff = min(backoff*2, beastReconnectMaxDelay)
		}
	}
}

// streamBeastOnce dials, reads, and feeds the airplanes store
// until either the context cancels (returns nil) or the stream
// errors (returns the wrapped error for streamBeast to log).
//
// A small watcher goroutine closes the connection on ctx.Done so
// a blocked Read unblocks immediately — no need to set a periodic
// SetReadDeadline on the conn.
func (a *ADSB) streamBeastOnce(ctx context.Context, planes *airplanes.Airplanes) error {
	conn, err := a.beastDialer.DialContext(ctx, "tcp", a.beastAddress)
	if err != nil {
		return fmt.Errorf("adsb: beast dial %q: %w", a.beastAddress, err)
	}

	slog.Info("adsb: beast connected", slog.String("address", a.beastAddress))

	a.connected.Store(true)
	defer a.connected.Store(false)

	cleanup := watchAndClose(ctx, conn)
	defer cleanup()

	reader := beast.NewReader(&countingReader{inner: conn, counter: &a.bytesIn})

	for {
		frame, err := reader.Frame()
		if err == nil {
			a.handleBeastFrame(frame, planes)

			continue
		}

		if ctx.Err() != nil {
			return nil
		}

		if errors.Is(err, io.EOF) {
			return errBeastServerHangup
		}

		return fmt.Errorf("adsb: beast read: %w", err)
	}
}

// handleBeastFrame promotes a beast.Frame into the demod.Frame
// shape handleFrame expects. The CRC residual is computed locally
// from the message bytes; WallTime is set to "now" because the
// 12 MHz tick counter in the BEAST envelope is chip-relative on
// the remote and not directly comparable to our wall clock.
func (a *ADSB) handleBeastFrame(frame beast.Frame, planes *airplanes.Airplanes) {
	crc := modes.CRCResidual(modes.Frame(frame.Bytes))

	a.handleFrame(demod.Frame{
		Bytes:    frame.Bytes,
		CRC:      crc,
		WallTime: time.Now(),
	}, planes)
}

// watchAndClose spawns a goroutine that closes conn when ctx
// fires, so a blocking Read returns immediately. The returned
// cleanup function tears down the watcher when streamBeastOnce
// exits normally, preventing the goroutine from leaking past
// the connection's lifetime.
func watchAndClose(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	return func() {
		close(stop)

		_ = conn.Close()
	}
}
