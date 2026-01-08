package adsb_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

func TestAltBaro_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    adsb.AltBaro
		wantErr bool
	}{
		{
			name:    "numeric altitude",
			input:   "35000",
			want:    35000,
			wantErr: false,
		},
		{
			name:    "ground altitude",
			input:   "\"ground\"",
			want:    0,
			wantErr: false,
		},
		{
			name:    "string numeric altitude",
			input:   "\"35000\"",
			want:    35000,
			wantErr: false,
		},
		{
			name:    "invalid string",
			input:   "\"invalid\"",
			wantErr: true,
		},
		{
			name:    "invalid numeric string",
			input:   "\"123a\"",
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   "{",
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var altBaro adsb.AltBaro

			err := json.Unmarshal([]byte(testCase.input), &altBaro)
			if (err != nil) != testCase.wantErr {
				t.Errorf("AltBaro.UnmarshalJSON() error = %v, wantErr %v", err, testCase.wantErr)

				return
			}

			if !testCase.wantErr && altBaro != testCase.want {
				t.Errorf("AltBaro.UnmarshalJSON() = %v, want %v", altBaro, testCase.want)
			}
		})
	}

	t.Run("unmarshal error", func(t *testing.T) {
		t.Parallel()

		var altBaro adsb.AltBaro

		err := altBaro.UnmarshalJSON([]byte("true")) // bool will fail both attempts
		if err == nil {
			t.Error("expected error for bool input")
		}
	})
}

func TestProcessAircraft(t *testing.T) {
	t.Parallel()

	hex := "ABCDEF"

	// Helper to float64 pointer
	f64Ptr := func(f float64) *float64 { return &f }
	// Helper to int pointer
	intPtr := func(i int) *int { return &i }

	for _, testCase := range getProcessAircraftTestCases(hex, f64Ptr, intPtr) {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			planes := airplanes.New()

			err := adsb.ProcessAircraft(testCase.aircraft, planes)
			if err != nil {
				t.Errorf("processAircraft() error = %v", err)

				return
			}

			testCase.verify(t, planes)
		})
	}
}

type processAircraftTestCase struct {
	name     string
	aircraft adsb.JSONAircraft
	verify   func(t *testing.T, p *airplanes.Airplanes)
}

func getProcessAircraftTestCases(
	hex string,
	f64Ptr func(float64) *float64,
	intPtr func(int) *int,
) []processAircraftTestCase {
	return []processAircraftTestCase{
		getFullUpdateTestCase(hex, f64Ptr, intPtr),
		getGeometricAltitudeTestCase(hex, f64Ptr, intPtr),
		getTrackTestCase(hex, f64Ptr),
		getGeometricVerticalRateTestCase(hex, intPtr),
	}
}

func getFullUpdateTestCase(
	hex string,
	f64Ptr func(float64) *float64,
	intPtr func(int) *int,
) processAircraftTestCase {
	return processAircraftTestCase{
		name: "full update",
		aircraft: adsb.JSONAircraft{
			Hex:                    hex,
			Flight:                 "DLH123 ",
			BarometricAltitude:     30000,
			GS:                     f64Ptr(450.5),
			TrueHeading:            f64Ptr(180.0),
			BarometricVerticalRate: intPtr(-1000),
			Lat:                    f64Ptr(52.5),
			Lon:                    f64Ptr(13.4),
			Squawk:                 "1234",
		},
		verify: func(t *testing.T, ps *airplanes.Airplanes) {
			t.Helper()

			plane, ok := ps.Get(hex)
			if !ok {
				t.Fatal("plane not found")
			}

			if plane.GetCallsign() != "DLH123" {
				t.Errorf("expected callsign DLH123, got %s", plane.GetCallsign())
			}

			if plane.GetAltitude() != 30000 {
				t.Errorf("expected altitude 30000, got %f", plane.GetAltitude())
			}

			if plane.GetVelocity() != 450.5 {
				t.Errorf("expected velocity 450.5, got %f", plane.GetVelocity())
			}

			if plane.GetHeading() != 180.0 {
				t.Errorf("expected heading 180.0, got %f", plane.GetHeading())
			}

			if plane.GetVertRate() != -1000 {
				t.Errorf("expected vert rate -1000, got %f", plane.GetVertRate())
			}

			if plane.GetLatitude() != 52.5 {
				t.Errorf("expected latitude 52.5, got %f", plane.GetLatitude())
			}

			if plane.GetLongitude() != 13.4 {
				t.Errorf("expected longitude 13.4, got %f", plane.GetLongitude())
			}

			if plane.GetSquawk() != "1234" {
				t.Errorf("expected squawk 1234, got %s", plane.GetSquawk())
			}
		},
	}
}

func getGeometricAltitudeTestCase(
	hex string,
	f64Ptr func(float64) *float64,
	intPtr func(int) *int,
) processAircraftTestCase {
	return processAircraftTestCase{
		name: "fallback to geometric altitude and magnetic heading",
		aircraft: adsb.JSONAircraft{
			Hex:               hex,
			GeometricAltitude: intPtr(31000),
			MagHeading:        f64Ptr(90.0),
		},
		verify: func(t *testing.T, ps *airplanes.Airplanes) {
			t.Helper()

			plane, _ := ps.Get(hex)
			if plane.GetAltitude() != 31000 {
				t.Errorf("expected altitude 31000, got %f", plane.GetAltitude())
			}

			if plane.GetHeading() != 90.0 {
				t.Errorf("expected heading 90.0, got %f", plane.GetHeading())
			}
		},
	}
}

func getTrackTestCase(hex string, f64Ptr func(float64) *float64) processAircraftTestCase {
	return processAircraftTestCase{
		name: "fallback to track",
		aircraft: adsb.JSONAircraft{
			Hex:   hex,
			Track: f64Ptr(45.0),
		},
		verify: func(t *testing.T, ps *airplanes.Airplanes) {
			t.Helper()

			plane, _ := ps.Get(hex)
			if plane.GetHeading() != 45.0 {
				t.Errorf("expected heading 45.0, got %f", plane.GetHeading())
			}
		},
	}
}

func getGeometricVerticalRateTestCase(hex string, intPtr func(int) *int) processAircraftTestCase {
	return processAircraftTestCase{
		name: "fallback to geometric vertical rate",
		aircraft: adsb.JSONAircraft{
			Hex:                   hex,
			GeometricVerticalRate: intPtr(500),
		},
		verify: func(t *testing.T, ps *airplanes.Airplanes) {
			t.Helper()

			plane, _ := ps.Get(hex)
			if plane.GetVertRate() != 500 {
				t.Errorf("expected vert rate 500, got %f", plane.GetVertRate())
			}
		},
	}
}

func TestProcessAircraft_NoAirplane(t *testing.T) {
	t.Parallel()

	err := adsb.ProcessAircraft(adsb.JSONAircraft{Hex: "ABC"}, nil)
	if err == nil {
		t.Error("expected error when planes list is nil")
	}
}

func TestADSB_Stream_Internal(t *testing.T) {
	t.Parallel()

	testStreamEmptyLines(t)
	testStreamInvalidJSON(t)
	testStreamScannerError(t)
	testStreamPruneCoverage(t)
	testStreamProcessAircraftError(t)
}

func testStreamEmptyLines(t *testing.T) {
	t.Helper()
	t.Run("empty lines", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			defer func() {
				_ = conn.Close()
			}()

			_, _ = conn.Write([]byte("\n\n"))

			time.Sleep(50 * time.Millisecond)
		}()

		adsbInstance := adsb.New(
			adsb.WithAddress(listener.Addr().String()),
			adsb.WithPruneFrequency(10*time.Millisecond),
		)

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		err := adsbInstance.Stream(ctx, airplanes.New())
		if err != nil {
			t.Errorf("expected nil for empty lines, got %v", err)
		}
	})
}

func testStreamInvalidJSON(t *testing.T) {
	t.Helper()
	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			defer func() {
				_ = conn.Close()
			}()

			_, _ = conn.Write([]byte("invalid json\n"))
		}()

		adsbInstance := adsb.New(adsb.WithAddress(listener.Addr().String()))

		err := adsbInstance.Stream(t.Context(), airplanes.New())
		if !errors.Is(err, adsb.ErrJSONParse()) {
			t.Errorf("expected errJSONParse, got %v", err)
		}
	})
}

func testStreamScannerError(t *testing.T) {
	t.Helper()
	t.Run("scanner error", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		adsbInstance := adsb.New(adsb.WithAddress(listener.Addr().String()))

		go func() {
			conn, _ := listener.Accept()
			if conn != nil {
				largeData := make([]byte, adsb.MaxBufferSize()+1)
				for i := range largeData {
					largeData[i] = 'a'
				}

				_, _ = conn.Write(largeData)

				_ = conn.Close()
			}
		}()

		err := adsbInstance.Stream(t.Context(), airplanes.New())
		if err == nil || !errors.Is(err, adsb.ErrJSONStream()) {
			t.Errorf("expected errJSONStream, got %v", err)
		}
	})
}

func testStreamPruneCoverage(t *testing.T) {
	t.Helper()
	t.Run("prune coverage", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		// Channel to signal when scanning should start
		startScan := make(chan struct{})

		go func() {
			conn, _ := listener.Accept()
			if conn != nil {
				<-startScan

				for range 50 {
					_, _ = conn.Write([]byte("{}\n"))

					time.Sleep(2 * time.Millisecond)
				}

				_ = conn.Close()
			}
		}()

		adsbInstance := adsb.New(
			adsb.WithAddress(listener.Addr().String()),
			adsb.WithPruneFrequency(10*time.Millisecond),
		)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			time.Sleep(80 * time.Millisecond)
			cancel()
			close(startScan) // Unblock scan goroutine if it was blocked on lines <- ...
		}()

		_ = adsbInstance.Stream(ctx, airplanes.New())
	})

	t.Run("scan context cancellation", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, _ := listener.Accept()
			if conn != nil {
				// Send a line so scanner.Scan() returns true
				_, _ = conn.Write([]byte("{}\n"))

				// Keep connection open but don't send more yet
				time.Sleep(100 * time.Millisecond)

				_ = conn.Close()
			}
		}()

		adsbInstance := adsb.New(adsb.WithAddress(listener.Addr().String()))
		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			_ = adsbInstance.Stream(ctx, airplanes.New())
		}()

		// Wait for one line to be received (or not)
		time.Sleep(20 * time.Millisecond)
		cancel()
	})
}

func testStreamProcessAircraftError(t *testing.T) {
	t.Helper()
	t.Run("processAircraft error", func(t *testing.T) {
		t.Parallel()

		var listenConfig net.ListenConfig

		listener, _ := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, _ := listener.Accept()
			if conn != nil {
				aircraft := adsb.JSONAircraft{Hex: "ABC"}
				data, _ := json.Marshal(aircraft)
				_, _ = conn.Write(append(data, '\n'))

				time.Sleep(50 * time.Millisecond)

				_ = conn.Close()
			}
		}()

		adsbInstance := adsb.New(adsb.WithAddress(listener.Addr().String()))

		err := adsbInstance.Stream(t.Context(), nil)
		if !errors.Is(err, adsb.ErrProcessAircraft()) {
			t.Errorf("expected errProcessAircraft, got %v", err)
		}
	})
}
