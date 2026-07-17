package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

// TestBuildADSBOptionsSelectsSource confirms buildADSBOptions routes
// each cfg to the right ingest branch, observed through the public
// source label. Replay wins over BEAST, BEAST over the local SDR.
func TestBuildADSBOptionsSelectsSource(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		cfg  cliConfig
		want string
	}{
		{"default is local SDR", cliConfig{}, "SDR"},
		{"beast address", cliConfig{beastAddress: "192.168.1.5:30005"}, "BEAST 192.168.1.5:30005"},
		{"replay path", cliConfig{replayIQPath: "/tmp/capture.iq"}, "Replay capture.iq"},
		{"replay wins over beast", cliConfig{replayIQPath: "/tmp/c.iq", beastAddress: "h:1"}, "Replay c.iq"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stream := adsb.New(buildADSBOptions(testCase.cfg, location.New(), nil)...)
			if got := stream.Source().Label; got != testCase.want {
				t.Errorf("Source().Label = %q, want %q", got, testCase.want)
			}
		})
	}
}

// errStubReceiverRead is the non-cancel read failure the stub receiver
// raises to stand in for a dongle that drops mid-stream.
var errStubReceiverRead = errors.New("stub receiver read failure")

// failAfterOpenReceiver opens fine and fails its first read, so the
// reconnect decision in the SDR branch is the only thing that keeps
// Stream alive afterwards.
type failAfterOpenReceiver struct{}

func (failAfterOpenReceiver) Read(context.Context, []byte) (int, error) {
	return 0, errStubReceiverRead
}

func (failAfterOpenReceiver) Close() error { return nil }

// TestBuildADSBOptionsSDRBranchEnablesReconnect proves the local-SDR
// branch carries WithSDRReconnect. We build the production SDR-branch
// options, then override only the receiver factory so Stream drives a
// stub that fails its first read. With reconnect wired, Stream parks
// in the backoff after that failure instead of returning the error;
// a prompt return would mean the option was missing.
//
// The 100 ms observation window sits an order of magnitude below the
// 1 s base backoff, so a parked Stream reliably stays silent while a
// reconnect-less Stream would return within microseconds.
func TestBuildADSBOptionsSDRBranchEnablesReconnect(t *testing.T) {
	t.Parallel()

	opts := append(
		buildADSBOptions(cliConfig{}, location.New(), nil),
		adsb.WithReceiverFactory(func() (adsb.Receiver, error) { return failAfterOpenReceiver{}, nil }),
	)

	stream := adsb.New(opts...)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- stream.Stream(ctx, airplanes.New()) }()

	select {
	case err := <-done:
		cancel()
		t.Fatalf("Stream returned %v within the window; SDR branch did not wire reconnect", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Stream err = %v, want nil after cancel during backoff", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}
}
