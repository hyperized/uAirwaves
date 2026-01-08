package adsb_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

func TestADSB_Stream(t *testing.T) {
	t.Parallel()

	// Create a local listener to simulate the ADSB service
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = listener.Close()
	}()

	addr := listener.Addr().String()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}

		defer func() {
			_ = conn.Close()
		}()

		_, _ = conn.Write([]byte(`{"hex":"ABCDEF","flight":"TEST123"}` + "\n"))

		time.Sleep(100 * time.Millisecond)
	}()

	adsbInstance := adsb.New(adsb.WithAddress(addr))
	planeList := airplanes.New()

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)

	go func() {
		errCh <- adsbInstance.Stream(ctx, planeList)
	}()

	time.Sleep(200 * time.Millisecond)

	plane, found := planeList.Get("ABCDEF")
	if !found {
		t.Error("expected aircraft ABCDEF to be processed")
	} else if plane.GetCallsign() != "TEST123" {
		t.Errorf("expected callsign TEST123, got %s", plane.GetCallsign())
	}

	cancel()

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Stream() unexpected error: %v", err)
	}
}

func TestADSB_Stream_Errors(t *testing.T) {
	t.Parallel()

	t.Run("dial error", func(t *testing.T) {
		t.Parallel()

		adsbInstance := adsb.New(adsb.WithAddress("127.0.0.1:1"))

		err := adsbInstance.Stream(t.Context(), airplanes.New())
		if err == nil {
			t.Error("expected error on dial failure")
		}
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		adsbInstance := adsb.New()
		if adsbInstance == nil {
			t.Fatal("expected ADSB instance")
		}
	})
}
