package airplane_test

import (
	"sync"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/pkg/airplane"
)

const (
	squitter = airplane.SourceExtendedSquitter
	parity   = airplane.SourceAddressParity

	appliesAtOnce = "extended squitter applies at once"
	callsignKLM   = "KLM1023"
	callsignTRA   = "TRA5X"
	callsignNoise = "QX#ZP7"
)

// epoch anchors every reported time in these tests. The rule never reads
// the wall clock, so any fixed instant will do.
var epoch = time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed test anchor.

type altitudeReport struct {
	offset time.Duration
	feet   float64
	src    airplane.Source
}

func reportAltitudes(plane *airplane.Airplane, reports []altitudeReport) {
	for _, report := range reports {
		plane.Update(airplane.WithReportedAltitude(report.feet, report.src, epoch.Add(report.offset)))
	}
}

type altitudeCase struct {
	name    string
	reports []altitudeReport
	want    float64
	wantAt  time.Duration // offset of the accepted report; -1 for none
}

func runAltitudeCases(t *testing.T, cases []altitudeCase) {
	t.Helper()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			plane := airplane.New(testICAO)
			reportAltitudes(plane, testCase.reports)

			snap := plane.GetSnapshot(airplane.WithoutHistory())
			if snap.Altitude != testCase.want {
				t.Errorf("Altitude = %v, want %v", snap.Altitude, testCase.want)
			}

			wantAt := time.Time{}
			if testCase.wantAt >= 0 {
				wantAt = epoch.Add(testCase.wantAt)
			}

			if !snap.AltitudeAt.Equal(wantAt) {
				t.Errorf("AltitudeAt = %v, want %v", snap.AltitudeAt, wantAt)
			}
		})
	}
}

// TestWithReportedAltitudeFirstAndConfirmed covers where an altitude comes
// from when there is nothing to compare it with.
func TestWithReportedAltitudeFirstAndConfirmed(t *testing.T) {
	t.Parallel()

	runAltitudeCases(t, []altitudeCase{
		{
			name:    appliesAtOnce,
			reports: []altitudeReport{{0, 37000, squitter}},
			want:    37000, wantAt: 0,
		},
		{
			name:    "extended squitter overrides anything, however far",
			reports: []altitudeReport{{0, 37000, squitter}, {time.Second, 600, squitter}},
			want:    600, wantAt: time.Second,
		},
		{
			name:    "first parity altitude ever is held",
			reports: []altitudeReport{{0, 12000, parity}},
			want:    0, wantAt: -1,
		},
		{
			name:    "heard only through DF4 and DF20: the second agreeing reply sets it",
			reports: []altitudeReport{{0, 12000, parity}, {4 * time.Second, 12100, parity}},
			want:    12100, wantAt: 4 * time.Second,
		},
		{
			name: "replies from one dwell confirm each other at any rate",
			reports: []altitudeReport{
				{0, 30000, parity}, {30 * time.Millisecond, 30000, parity},
			},
			want: 30000, wantAt: 30 * time.Millisecond,
		},
		{
			name:    "a second reply outside the confirmation window does not confirm",
			reports: []altitudeReport{{0, 12000, parity}, {11 * time.Second, 12000, parity}},
			want:    0, wantAt: -1,
		},
		{
			name: "a disagreeing reply replaces the candidate, and the next one confirms it",
			reports: []altitudeReport{
				{0, 45000, parity}, {2 * time.Second, 12000, parity}, {4 * time.Second, 12050, parity},
			},
			want: 12050, wantAt: 4 * time.Second,
		},
	})
}

// TestWithReportedAltitudeAgainstCurrent covers a reply judged against the
// altitude the aircraft already has.
func TestWithReportedAltitudeAgainstCurrent(t *testing.T) {
	t.Parallel()

	runAltitudeCases(t, []altitudeCase{
		{
			name:    "descent at the limit is accepted from one reply",
			reports: []altitudeReport{{0, 30000, squitter}, {5 * time.Second, 29300, parity}},
			want:    29300, wantAt: 5 * time.Second,
		},
		{
			name:    "climb at the limit is accepted from one reply",
			reports: []altitudeReport{{0, 10000, squitter}, {10 * time.Second, 11300, parity}},
			want:    11300, wantAt: 10 * time.Second,
		},
		{
			name:    "a step beyond the limit is held",
			reports: []altitudeReport{{0, 10000, squitter}, {10 * time.Second, 11350, parity}},
			want:    10000, wantAt: 0,
		},
		{
			name:    "a change within the slack is accepted at once",
			reports: []altitudeReport{{0, 10000, squitter}, {0, 10300, parity}},
			want:    10300, wantAt: 0,
		},
		{
			name: "the cruising aircraft's phantoms are held while its squitters are fresh",
			reports: []altitudeReport{
				{0, 37000, squitter}, {time.Second, 21150, parity}, {2 * time.Second, 7375, parity},
				{3 * time.Second, 600, parity}, {4 * time.Second, 600, parity},
			},
			want: 37000, wantAt: 0,
		},
	})
}

// TestWithReportedAltitudeOverTime covers squitter priority, fast descents
// and what a long silence does to the current altitude.
func TestWithReportedAltitudeOverTime(t *testing.T) {
	t.Parallel()

	runAltitudeCases(t, []altitudeCase{
		{
			name: "two agreeing phantoms after the squitters went quiet are applied, and the next squitter undoes them",
			reports: []altitudeReport{
				{0, 37000, squitter}, {20 * time.Second, 5500, parity}, {25 * time.Second, 6250, parity},
				{26 * time.Second, 37000, squitter},
			},
			want: 37000, wantAt: 26 * time.Second,
		},
		{
			name: "emergency descent through extended squitters is followed at once",
			reports: []altitudeReport{
				{0, 35000, squitter}, {30 * time.Second, 30000, squitter}, {60 * time.Second, 25000, squitter},
			},
			want: 25000, wantAt: 60 * time.Second,
		},
		{
			name: "emergency descent heard only through DF4 is followed every other reply",
			reports: []altitudeReport{
				{0, 35000, parity}, {50 * time.Millisecond, 35000, parity},
				{5 * time.Second, 34167, parity}, {10 * time.Second, 33333, parity},
			},
			want: 33333, wantAt: 10 * time.Second,
		},
		{
			name: "after a long silence the stale altitude cannot vouch for the next reply",
			reports: []altitudeReport{
				{0, 30000, squitter}, {2 * time.Minute, 30100, parity},
			},
			want: 30000, wantAt: 0,
		},
		{
			name: "after a long silence two agreeing replies replace the stale altitude",
			reports: []altitudeReport{
				{0, 30000, squitter}, {2 * time.Minute, 18000, parity}, {2*time.Minute + 4*time.Second, 17900, parity},
			},
			want: 17900, wantAt: 2*time.Minute + 4*time.Second,
		},
		{
			name: "negative altitudes follow the same rule",
			reports: []altitudeReport{
				{0, -25, squitter}, {time.Second, -50, parity},
			},
			want: -50, wantAt: time.Second,
		},
		{
			name:    "the lowest code, -1000 ft, is held when it does not fit",
			reports: []altitudeReport{{0, 16350, squitter}, {time.Second, -1000, parity}},
			want:    16350, wantAt: 0,
		},
		{
			name:    "an earlier-stamped reply is judged as if no time had passed",
			reports: []altitudeReport{{10 * time.Second, 10000, squitter}, {9 * time.Second, 10250, parity}},
			want:    10250, wantAt: 9 * time.Second,
		},
	})
}

func TestWithAltitudeLeavesAltitudeAtAlone(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	plane.Update(airplane.WithAltitude(12000))

	snap := plane.GetSnapshot()
	if snap.Altitude != 12000 || !snap.AltitudeAt.IsZero() {
		t.Errorf("got %v at %v, want 12000 with no time", snap.Altitude, snap.AltitudeAt)
	}

	// An untimed altitude vouches for nothing.
	plane.Update(airplane.WithReportedAltitude(12025, parity, epoch))

	if got := plane.GetSnapshot().Altitude; got != 12000 {
		t.Errorf("Altitude = %v, want 12000 held", got)
	}
}

type identityReport struct {
	offset time.Duration
	value  string
	src    airplane.Source
}

func TestWithReportedSquawk(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name          string
		reports       []identityReport
		want          string
		wantEmergency bool
	}{
		{
			name: appliesAtOnce, reports: []identityReport{{0, "7700", squitter}},
			want: "7700", wantEmergency: true,
		},
		{name: "a single phantom 7700 is held", reports: []identityReport{{0, "7700", parity}}},
		{
			name:    "a phantom 7700 over a known squawk is held",
			reports: []identityReport{{0, "1000", squitter}, {time.Second, "7700", parity}},
			want:    "1000",
		},
		{
			name:    "two replies with 7700 declare the emergency",
			reports: []identityReport{{0, "7700", parity}, {12 * time.Second, "7700", parity}},
			want:    "7700", wantEmergency: true,
		},
		{
			name:    "two different codes confirm nothing",
			reports: []identityReport{{0, "1000", parity}, {time.Second, "7700", parity}},
		},
		{
			name:    "a repeat outside the window confirms nothing",
			reports: []identityReport{{0, "7700", parity}, {31 * time.Second, "7700", parity}},
		},
		{
			name:    "a reply repeating the current squawk keeps it",
			reports: []identityReport{{0, "1000", squitter}, {time.Second, "1000", parity}},
			want:    "1000",
		},
		{
			name: "a confirmed change clears an emergency",
			reports: []identityReport{
				{0, "7700", squitter}, {time.Second, "1000", parity}, {2 * time.Second, "1000", parity},
			},
			want: "1000",
		},
		{name: "empty is ignored", reports: []identityReport{{0, "", squitter}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			plane := airplane.New(testICAO)
			for _, report := range testCase.reports {
				plane.Update(airplane.WithReportedSquawk(report.value, report.src, epoch.Add(report.offset)))
			}

			snap := plane.GetSnapshot()
			if snap.Squawk != testCase.want || snap.Emergency != testCase.wantEmergency {
				t.Errorf("Squawk = %q emergency %v, want %q emergency %v",
					snap.Squawk, snap.Emergency, testCase.want, testCase.wantEmergency)
			}
		})
	}
}

func TestWithReportedCallsign(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		reports []identityReport
		want    string
	}{
		{name: appliesAtOnce, reports: []identityReport{{0, callsignKLM, squitter}}, want: callsignKLM},
		{name: "a single Comm-B callsign is held", reports: []identityReport{{0, callsignNoise, parity}}},
		{
			name:    "a Comm-B callsign repeating the current one keeps it",
			reports: []identityReport{{0, callsignKLM, squitter}, {time.Second, callsignKLM, parity}},
			want:    callsignKLM,
		},
		{
			name:    "a phantom callsign over a known one is held",
			reports: []identityReport{{0, callsignKLM, squitter}, {time.Second, callsignNoise, parity}},
			want:    callsignKLM,
		},
		{
			name:    "two agreeing Comm-B replies set it",
			reports: []identityReport{{0, callsignTRA, parity}, {9 * time.Second, callsignTRA, parity}},
			want:    callsignTRA,
		},
		{name: "empty is ignored", reports: []identityReport{{0, "", squitter}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			plane := airplane.New(testICAO)
			for _, report := range testCase.reports {
				plane.Update(airplane.WithReportedCallsign(report.value, report.src, epoch.Add(report.offset)))
			}

			if got := plane.GetSnapshot().Callsign; got != testCase.want {
				t.Errorf("Callsign = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestReportedAltitudeConcurrent runs reports against snapshot readers so
// the race detector sees the trust state touched only under the lock.
func TestReportedAltitudeConcurrent(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)

	var group sync.WaitGroup

	const workers = 8

	for worker := range workers {
		group.Go(func() {
			for step := range 100 {
				at := epoch.Add(time.Duration(step) * time.Second)
				src := airplane.Source(worker % 2) //nolint:gosec // worker%2 is 0 or 1.
				plane.Update(
					airplane.WithReportedAltitude(float64(30000+step), src, at),
					airplane.WithReportedSquawk("1000", src, at),
					airplane.WithReportedCallsign(callsignKLM, src, at),
				)
				_ = plane.GetSnapshot(airplane.WithoutHistory())
			}
		})
	}

	group.Wait()

	if got := plane.GetSnapshot().Squawk; got != "1000" {
		t.Errorf("Squawk = %q, want 1000", got)
	}
}
