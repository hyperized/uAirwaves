package adsb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyperized/demod1090/demod"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

func TestNewFileReceiverMissingPathReturnsWrappedError(t *testing.T) {
	t.Parallel()

	rcv, err := NewFileReceiver(filepath.Join(t.TempDir(), "no-such-file.iq"))
	if err == nil {
		_ = rcv.Close() //nolint:errcheck // defensive guard; only reached if behaviour regressed.

		t.Fatal("NewFileReceiver: want error for missing path")
	}

	if !errors.Is(err, errOpenReplay) {
		t.Errorf("NewFileReceiver err = %v, want wraps errOpenReplay", err)
	}
}

func TestFileReceiverReadStreamsBytesThenSurfacesEOF(t *testing.T) {
	t.Parallel()

	const payloadBytes = 1024

	dir := t.TempDir()
	path := filepath.Join(dir, "cap.iq")

	payload := make([]byte, payloadBytes)
	for i := range payload {
		payload[i] = byte(i & 0xFF) //nolint:gosec // bounded by mask.
	}

	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rcv, err := NewFileReceiver(path)
	if err != nil {
		t.Fatalf("NewFileReceiver: %v", err)
	}

	defer func() { _ = rcv.Close() }()

	buf := make([]byte, len(payload))

	count, err := rcv.Read(t.Context(), buf)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}

	if count != len(payload) {
		t.Errorf("Read count = %d, want %d", count, len(payload))
	}

	for i, gotByte := range buf {
		if gotByte != payload[i] {
			t.Errorf("byte %d = %#x, want %#x", i, gotByte, payload[i])

			break
		}
	}

	// Second read must surface EOF as ErrReplayEnded so callers
	// can distinguish "capture exhausted" from "operator cancelled".
	if _, err := rcv.Read(t.Context(), buf); !errors.Is(err, ErrReplayEnded) {
		t.Errorf("second Read err = %v, want ErrReplayEnded", err)
	}
}

func TestFileReceiverReadOnClosedFileSurfacesError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap.iq")

	if err := os.WriteFile(path, []byte("anything"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rcv, err := NewFileReceiver(path)
	if err != nil {
		t.Fatalf("NewFileReceiver: %v", err)
	}

	if err := rcv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read after Close: *os.File returns ErrClosed (not EOF), so
	// the receiver wraps it in adsb: replay read.
	_, err = rcv.Read(t.Context(), make([]byte, 8)) //nolint:mnd // arbitrary buffer size.
	if err == nil {
		t.Fatal("Read on closed file: want error, got nil")
	}

	if errors.Is(err, ErrReplayEnded) {
		t.Errorf("Read on closed file = %v; should not be ErrReplayEnded (only EOF maps that way)", err)
	}
}

func TestFileReceiverHonoursContextCancel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap.iq")

	if err := os.WriteFile(path, []byte("anything"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rcv, err := NewFileReceiver(path)
	if err != nil {
		t.Fatalf("NewFileReceiver: %v", err)
	}

	defer func() { _ = rcv.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	const bufSize = 64

	if _, err := rcv.Read(ctx, make([]byte, bufSize)); !errors.Is(err, context.Canceled) {
		t.Errorf("Read err = %v, want context.Canceled", err)
	}
}

func TestFileReceiverCloseTwiceIsSurfacedAsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap.iq")

	if err := os.WriteFile(path, []byte{0x00}, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rcv, err := NewFileReceiver(path)
	if err != nil {
		t.Fatalf("NewFileReceiver: %v", err)
	}

	if err := rcv.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second close on an already-closed *os.File errors; the
	// receiver must wrap that error rather than swallow it.
	if err := rcv.Close(); err == nil {
		t.Error("second Close: want non-nil error, got nil")
	}
}

// TestStreamMapsReplayEndedToNil locks the documented contract on
// Stream: when the receiver returns ErrReplayEnded the loop exits
// cleanly with nil so the UI shutdown path is identical to a
// user-initiated context cancellation. Without this branch the
// replay path would surface an "adsb: read" wrap to the error
// channel and the UI would log a spurious fatal at end-of-file.
func TestStreamMapsReplayEndedToNil(t *testing.T) {
	t.Parallel()

	rcv := &fakeReceiver{
		reads: []fakeRead{{err: ErrReplayEnded}},
	}
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	if err := stream.Stream(t.Context(), airplanes.New()); err != nil {
		t.Errorf("Stream err = %v, want nil (ErrReplayEnded must map to clean exit)", err)
	}
}

func TestStreamThroughFileReceiverDecodesIdent(t *testing.T) {
	t.Parallel()

	// End-to-end smoke for the replay path: write IQ-shaped bytes
	// to a temp file, wire NewFileReceiver into Stream, and have
	// the demod fake produce a known DF 17 ident frame off the
	// first Process call. Verifies the file-receiver loop and the
	// adsb pipeline cooperate cleanly.
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.iq")

	if err := os.WriteFile(path, make([]byte, 4096), 0o600); err != nil { //nolint:mnd // chunk-sized fixture.
		t.Fatalf("write fixture: %v", err)
	}

	frameBytes := []byte{
		0x8D, 0x40, 0x62, 0x1D,
		0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0,
		0x57, 0x60, 0x98,
	}

	dem := &fakeDemodulator{
		frames: []demod.Frame{{Bytes: frameBytes, DF: 17}},
	}

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return NewFileReceiver(path) }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	)

	planes := airplanes.New()
	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if _, ok := planes.Get("40621D"); !ok {
		t.Error("plane 40621D not registered after replay")
	}
}
