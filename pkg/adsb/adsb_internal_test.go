package adsb

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestScan_ContextCancelledWhileBlocked(t *testing.T) {
	t.Parallel()

	// Create a pipe to simulate connection
	server, client := net.Pipe()

	defer func() {
		_ = server.Close()
		_ = client.Close()
	}()

	adsbInstance := &ADSB{
		connection: client,
	}

	ctx, cancel := context.WithCancel(t.Context())
	lines := make(chan string) // unbuffered, will block scan
	errs := make(chan error, 1)

	// Send one line from server
	go func() {
		_, _ = server.Write([]byte("test line\n"))
	}()

	// Start scan in goroutine
	scanFinished := make(chan struct{})

	go func() {
		adsbInstance.scan(ctx, lines, errs)
		close(scanFinished)
	}()

	// Wait a bit to ensure scan is blocked on lines <- ...
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for scan to finish
	select {
	case <-scanFinished:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("scan did not finish after context cancellation")
	}
}
