package adsb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hyperized/demod1090/beast"
	"github.com/hyperized/modes"
)

// encodedBeastFrames returns the wire-encoded BEAST stream for n
// copies of a fixed DF 17 payload. One contiguous slice the caller
// can hand to bytes.NewReader or an io.Pipe writer.
func encodedBeastFrames(t *testing.T, count int) []byte {
	t.Helper()

	payload := modes.AppendCRC24([]byte{
		0x8D, 0x40, 0x62, 0x1D,
		0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0,
	})

	var buf bytes.Buffer

	for idx := range count {
		encoded, err := beast.Encode(nil, payload, uint64(idx), 0)
		if err != nil {
			t.Fatalf("encode frame %d: %v", idx, err)
		}

		buf.Write(encoded)
	}

	return buf.Bytes()
}

// TestReadBeastFramesForwardsAllFramesThenSignalsEOF feeds a
// fixed-size stream end-to-end and verifies every parsed frame
// reaches the channel and the underlying io.EOF surfaces on errCh.
func TestReadBeastFramesForwardsAllFramesThenSignalsEOF(t *testing.T) {
	t.Parallel()

	const wantFrames = 8

	src := bytes.NewReader(encodedBeastFrames(t, wantFrames))
	reader := beast.NewReader(src)

	frames := make(chan beast.Frame, beastFrameQueueDepth)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go readBeastFrames(ctx, reader, frames, errCh)

	got := 0
	for range frames {
		got++
	}

	if got != wantFrames {
		t.Errorf("received %d frames, want %d", got, wantFrames)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Errorf("errCh = %v, want io.EOF", err)
		}
	default:
		t.Error("errCh empty after frames closed, want io.EOF")
	}
}

// TestReadBeastFramesFillsBufferWithoutHandlerDrain is the
// structural test the refactor exists for: with no handler
// consuming the channel, the reader still parses up to the
// channel's capacity from the source. Demonstrates that read
// throughput is decoupled from handle throughput up to the
// buffer's depth — which is what prevents the demod1090 BEAST
// server from logging "slow client" during transient handler
// stalls.
func TestReadBeastFramesFillsBufferWithoutHandlerDrain(t *testing.T) {
	t.Parallel()

	// Feed exactly buffer-depth frames so the reader can pull
	// every one of them into the channel without blocking on send.
	const wantFrames = beastFrameQueueDepth

	src := bytes.NewReader(encodedBeastFrames(t, wantFrames))
	reader := beast.NewReader(src)

	frames := make(chan beast.Frame, beastFrameQueueDepth)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})

	go func() {
		readBeastFrames(ctx, reader, frames, errCh)
		close(done)
	}()

	// Wait for the reader to finish (it hits EOF after the last
	// frame). With no handler running, the proof is that the close
	// happens at all — if read and handle were coupled, the reader
	// would deadlock on the very first channel send.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readBeastFrames did not finish within 2s — read appears coupled to handler drain")
	}

	if got := len(frames); got != wantFrames {
		t.Errorf("channel buffered %d frames, want %d (reader did not pump independently)", got, wantFrames)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Errorf("errCh = %v, want io.EOF after source drained", err)
		}
	default:
		t.Error("errCh empty after reader returned, want io.EOF")
	}
}

// TestReadBeastFramesCtxCancelUnblocksFullChannel covers the
// shutdown path when the channel is full and the reader is
// blocked on send. ctx cancellation must release the goroutine
// cleanly without writing to errCh.
func TestReadBeastFramesCtxCancelUnblocksFullChannel(t *testing.T) {
	t.Parallel()

	// 2× the buffer depth so the reader is guaranteed to be
	// blocked on send when we cancel.
	src := bytes.NewReader(encodedBeastFrames(t, 2*beastFrameQueueDepth))
	reader := beast.NewReader(src)

	frames := make(chan beast.Frame, beastFrameQueueDepth)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		readBeastFrames(ctx, reader, frames, errCh)
		close(done)
	}()

	// Wait until the channel is full so we know the reader is
	// parked on the send branch of its select.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(frames) < beastFrameQueueDepth {
		time.Sleep(5 * time.Millisecond)
	}

	if got := len(frames); got != beastFrameQueueDepth {
		t.Fatalf("channel filled to %d, want %d before cancel", got, beastFrameQueueDepth)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readBeastFrames did not exit after ctx cancel — full-channel deadlock")
	}

	// ctx-cancel exit must NOT write to errCh — the caller relies
	// on ctx.Err() being the canonical "we cancelled" signal. The
	// done close above already proves frames was closed (deferred
	// inside readBeastFrames), so no separate drain is required.
	select {
	case err := <-errCh:
		t.Errorf("errCh received %v on ctx-cancel exit, want no value", err)
	default:
	}
}
