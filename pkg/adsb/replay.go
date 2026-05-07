package adsb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// errOpenReplay is the static sentinel for the "couldn't open the
// replay file" failure mode. err113 forbids ad-hoc errors.New from
// fmt.Errorf; static + %w keeps callers branchable.
var errOpenReplay = errors.New("adsb: open replay")

// NewFileReceiver opens path for reading and returns a Receiver
// that streams the file's contents through Read calls. The file
// is expected to hold raw interleaved unsigned 8-bit IQ samples
// (rtl_sdr / dump1090 --ifile / rtl-probe --capture format).
//
// EOF is surfaced as context.Canceled rather than io.EOF so the
// Stream loop in pkg/adsb treats end-of-replay as a clean shutdown.
//
// Useful for off-line A/B testing of the demod chain on hosts
// without an SDR, and for the UAIRWAVES_REPLAY_IQ workflow that
// runs the full TUI deterministically against a captured fixture.
//
//nolint:ireturn // factory: returning the interface is the seam main wires up.
func NewFileReceiver(path string) (Receiver, error) {
	//nolint:gosec // G304: replay path is operator-supplied by design (CLI/env).
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errOpenReplay, err)
	}

	return &fileReceiver{file: file}, nil
}

// fileReceiver is the package-private *os.File-backed Receiver.
type fileReceiver struct {
	file *os.File
}

// Read pulls the next chunk from the file. EOF is mapped to
// context.Canceled so the Stream loop's cancel-arm fires.
func (r *fileReceiver) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("adsb: replay context: %w", err)
	}

	count, err := r.file.Read(p)
	if errors.Is(err, io.EOF) {
		return count, context.Canceled
	}

	if err != nil {
		return count, fmt.Errorf("adsb: replay read: %w", err)
	}

	return count, nil
}

// Close releases the underlying file handle.
func (r *fileReceiver) Close() error {
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("adsb: replay close: %w", err)
	}

	return nil
}
