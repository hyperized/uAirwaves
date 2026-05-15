package check_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/internal/check"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

const fix3D = "3D fix"

var (
	errTestGPS   = errors.New("test: gps gone")
	errTestIO    = errors.New("test: io fail")
	errTestPanic = errors.New("test: boom")
)

func TestDefaultThresholds(t *testing.T) {
	t.Parallel()

	got := check.DefaultThresholds()

	if got.MinFrames != 1 {
		t.Errorf("MinFrames = %d, want 1", got.MinFrames)
	}

	if got.MinPlanes != 1 {
		t.Errorf("MinPlanes = %d, want 1", got.MinPlanes)
	}
}

func TestBuildReport_PassesAllThresholds(t *testing.T) {
	t.Parallel()

	snaps := []airplane.Snapshot{
		{ICAO: "ABC123", Callsign: "KLM1", Altitude: 35000, Latitude: 52.0, Longitude: 4.0},
		{ICAO: "DEF456", Callsign: "DLH9", Altitude: 38000, Latitude: 53.0, Longitude: 5.0},
		{ICAO: "GHI789", Callsign: "AFR2", Altitude: 0, Latitude: 0, Longitude: 0},
	}

	report := check.BuildReport(check.Inputs{
		DurationSeconds: 10,
		Stats: adsb.Stats{
			TotalFrames: 42, RecoveredFrames: 1,
			CallsignsDecoded: 5, CallsignsApplied: 4,
		},
		Snapshots:    snaps,
		ReceiverLat:  52.0,
		ReceiverLon:  4.0,
		GPSFix:       true,
		GPSMode:      fix3D,
		GPSAltitudeM: 12.3,
		Thresholds:   check.DefaultThresholds(),
	})

	if !report.OK {
		t.Fatalf("expected OK report, got failures %v", report.Thresholds.Failures)
	}

	if report.ADSB.Tracked != 3 {
		t.Errorf("Tracked = %d, want 3", report.ADSB.Tracked)
	}

	if report.ADSB.Positioned != 2 {
		t.Errorf("Positioned = %d, want 2", report.ADSB.Positioned)
	}

	if report.GPS.Mode != fix3D || !report.GPS.Fix {
		t.Errorf("GPS = %+v, want 3D fix with Fix=true", report.GPS)
	}

	if report.Nearest == nil || report.Nearest.Callsign != "KLM1" {
		t.Errorf("Nearest = %+v, want KLM1", report.Nearest)
	}

	if report.Highest == nil || report.Highest.Callsign != "DLH9" || report.Highest.AltitudeFt != 38000 {
		t.Errorf("Highest = %+v, want DLH9 @38000", report.Highest)
	}
}

func TestBuildReport_FailsThresholds(t *testing.T) {
	t.Parallel()

	report := check.BuildReport(check.Inputs{
		DurationSeconds: 5,
		Stats:           adsb.Stats{TotalFrames: 0},
		Snapshots:       nil,
		Thresholds:      check.DefaultThresholds(),
	})

	if report.OK {
		t.Fatal("expected !OK")
	}

	if len(report.Thresholds.Failures) != 2 {
		t.Fatalf("Failures = %v, want frames + planes", report.Thresholds.Failures)
	}

	if report.Nearest != nil || report.Highest != nil {
		t.Errorf("expected no Nearest/Highest when no snapshots, got %+v / %+v",
			report.Nearest, report.Highest)
	}
}

func TestBuildReport_PrefersICAOWhenNoCallsign(t *testing.T) {
	t.Parallel()

	report := check.BuildReport(check.Inputs{
		DurationSeconds: 1,
		Stats:           adsb.Stats{TotalFrames: 1},
		Snapshots: []airplane.Snapshot{
			{ICAO: "XYZ000", Altitude: 100, Latitude: 1, Longitude: 1},
		},
		ReceiverLat: 2, ReceiverLon: 2,
		Thresholds: check.DefaultThresholds(),
	})

	if report.Nearest == nil || report.Nearest.Callsign != "XYZ000" {
		t.Errorf("Nearest = %+v, want ICAO XYZ000", report.Nearest)
	}
}

func TestBuildReport_SkipsInvalidDistance(t *testing.T) {
	t.Parallel()

	report := check.BuildReport(check.Inputs{
		DurationSeconds: 1,
		Stats:           adsb.Stats{TotalFrames: 1},
		Snapshots: []airplane.Snapshot{
			{ICAO: "ABC123", Callsign: "X", Altitude: 100, Latitude: 1, Longitude: 1},
		},
		// Receiver at (0,0) -> HaversineDistance returns MaxFloat64
		// for every plane and they should be excluded from Positioned.
		ReceiverLat: 0, ReceiverLon: 0,
		Thresholds: check.DefaultThresholds(),
	})

	if report.ADSB.Positioned != 0 {
		t.Errorf("Positioned = %d, want 0 (all distances invalid)", report.ADSB.Positioned)
	}

	if report.Nearest != nil {
		t.Errorf("Nearest = %+v, want nil when no valid distance", report.Nearest)
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantDur time.Duration
		errFrag string
	}{
		{"valid 10s", "10s", false, 10 * time.Second, ""},
		{"valid 1m", "1m", false, 1 * time.Minute, ""},
		{"too short", "500ms", true, 0, "below minimum"},
		{"too long", "10m", true, 0, "above maximum"},
		{"garbage", "ten seconds", true, 0, "parse check duration"},
		{"empty", "", true, 0, "parse check duration"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dur, err := check.ParseDuration(testCase.input)

			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dur=%s", dur)
				}

				if !strings.Contains(err.Error(), testCase.errFrag) {
					t.Errorf("err = %q, want fragment %q", err.Error(), testCase.errFrag)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if dur != testCase.wantDur {
				t.Errorf("dur = %s, want %s", dur, testCase.wantDur)
			}
		})
	}
}

func TestLogStart(t *testing.T) {
	t.Parallel()

	// Just ensure it does not panic. Output is via slog default
	// handler — capturing stderr in parallel-safe tests is fragile;
	// the goal here is "executes once" coverage.
	check.LogStart(time.Second)
}

func TestRun_WritesJSONAndExitsAfterDuration(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	planes.Ensure("ABC123")

	loc := location.New(
		location.WithLatitude(52.0),
		location.WithLongitude(4.0),
		location.WithMode(3),
	)
	stream := adsb.New(adsb.WithLocation(loc))

	var buf bytes.Buffer

	noopWorker := func(ctx context.Context) error {
		<-ctx.Done()

		return nil
	}

	start := time.Now()

	report, passed, err := check.Run(t.Context(), check.Options{
		Duration:   2 * time.Second,
		Thresholds: check.Thresholds{MinFrames: 0, MinPlanes: 1},
		Output:     &buf,
		Stream:     stream,
		Planes:     planes,
		Location:   loc,
		StartGPS:   noopWorker,
		StartADSB:  noopWorker,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Errorf("Run returned in %s, expected ≥ 2s", elapsed)
	}

	if !passed || !report.OK {
		t.Errorf("expected OK report, failures = %v", report.Thresholds.Failures)
	}

	if !report.GPS.Fix || report.GPS.Mode != fix3D {
		t.Errorf("GPS = %+v, want fix true mode 3D fix", report.GPS)
	}

	var decoded check.Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if decoded.ADSB.Tracked != 1 {
		t.Errorf("decoded Tracked = %d, want 1", decoded.ADSB.Tracked)
	}
}

func TestRun_PropagatesWorkerError(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	stream := adsb.New(adsb.WithLocation(loc))

	var buf bytes.Buffer

	report, passed, err := check.Run(t.Context(), check.Options{
		Duration:   5 * time.Second,
		Thresholds: check.DefaultThresholds(),
		Output:     &buf,
		Stream:     stream,
		Planes:     planes,
		Location:   loc,
		StartGPS:   func(context.Context) error { return errTestGPS },
		StartADSB:  blockingWorker,
	})

	if !errors.Is(err, errTestGPS) {
		t.Errorf("err = %v, want %v", err, errTestGPS)
	}

	if passed || report.OK {
		t.Error("expected !OK when no frames/planes")
	}

	if buf.Len() == 0 {
		t.Error("expected JSON output even on early exit")
	}
}

func TestRun_CancelledParent(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	stream := adsb.New(adsb.WithLocation(loc))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var buf bytes.Buffer

	report, _, err := check.Run(ctx, check.Options{
		Duration:   5 * time.Second,
		Thresholds: check.DefaultThresholds(),
		Output:     &buf,
		Stream:     stream,
		Planes:     planes,
		Location:   loc,
		StartGPS:   blockingWorker,
		StartADSB:  blockingWorker,
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}

	if report.DurationSeconds != 5 {
		t.Errorf("DurationSeconds = %f, want 5", report.DurationSeconds)
	}
}

func TestRun_WriteErrorSurfaces(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	stream := adsb.New(adsb.WithLocation(loc))

	report, passed, err := check.Run(t.Context(), check.Options{
		Duration:   100 * time.Millisecond,
		Thresholds: check.DefaultThresholds(),
		Output:     errWriter{},
		Stream:     stream,
		Planes:     planes,
		Location:   loc,
		StartGPS:   blockingWorker,
		StartADSB:  blockingWorker,
	})
	if err == nil {
		t.Fatal("expected write error to surface")
	}

	if passed || report.OK {
		t.Error("OK should be false when nothing was observed")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errTestIO }

func TestRun_RecoversFromPanic(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	stream := adsb.New(adsb.WithLocation(loc))

	var buf bytes.Buffer

	_, _, err := check.Run(t.Context(), check.Options{
		Duration:   2 * time.Second,
		Thresholds: check.DefaultThresholds(),
		Output:     &buf,
		Stream:     stream,
		Planes:     planes,
		Location:   loc,
		StartGPS:   func(context.Context) error { panic(errTestPanic) },
		StartADSB:  blockingWorker,
	})
	if !errors.Is(err, errTestPanic) {
		t.Errorf("err = %v, want it to wrap %v", err, errTestPanic)
	}
}

func blockingWorker(ctx context.Context) error {
	<-ctx.Done()

	return nil
}
