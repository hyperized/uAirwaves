package airplanes_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
)

func TestNew(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	if list == nil {
		t.Fatal("expected New() to return a non-nil instance")
	}

	if list.Count() != 0 {
		t.Errorf("expected empty airplanes list, got %d", list.Count())
	}
}

func TestEnsureAndGet(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	icao := "ABCDEF"

	list.Ensure(icao)

	if list.Count() != 1 {
		t.Errorf("expected 1 plane, got %d", list.Count())
	}

	plane, found := list.Get(icao)
	if !found {
		t.Error("expected to find plane")
	}

	if plane.GetSnapshot().ICAO != icao {
		t.Errorf("expected ICAO %s, got %s", icao, plane.GetSnapshot().ICAO)
	}

	// Ensure again shouldn't add a new one
	list.Ensure(icao)

	if list.Count() != 1 {
		t.Errorf("expected still 1 plane after re-ensuring, got %d", list.Count())
	}

	_, found = list.Get("NONEXISTENT")
	if found {
		t.Error("expected not to find nonexistent plane")
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	icaoOld := "OLD"
	icaoNew := "NEW"

	list.Ensure(icaoOld)
	list.Ensure(icaoNew)

	planeOld, _ := list.Get(icaoOld)
	planeOld.Update(airplane.WithLastUpdate(time.Now().Add(-2 * time.Hour)))

	list.Prune(1 * time.Hour)

	if list.Count() != 1 {
		t.Errorf("expected 1 plane after pruning, got %d", list.Count())
	}

	if _, found := list.Get(icaoNew); !found {
		t.Error("expected NEW plane to remain")
	}

	if _, found := list.Get(icaoOld); found {
		t.Error("expected OLD plane to be pruned")
	}
}

func TestSorted(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	receiverLat, receiverLon := 52.0, 13.0

	// p1: Closer
	list.Ensure("P1")
	plane1, _ := list.Get("P1")
	plane1.Update(airplane.WithLatitude(52.1), airplane.WithLongitude(13.1))

	// p2: Farther
	list.Ensure("P2")
	plane2, _ := list.Get("P2")
	plane2.Update(airplane.WithLatitude(53.0), airplane.WithLongitude(14.0))

	// p3: Same distance as plane1, but older update
	list.Ensure("P3")
	plane3, _ := list.Get("P3")
	plane3.Update(airplane.WithLatitude(52.1), airplane.WithLongitude(13.1))
	plane3.Update(airplane.WithLastUpdate(time.Now().Add(-10 * time.Minute)))

	// p4: Unknown position (0,0) - should be last
	list.Ensure("P4")
	plane4, _ := list.Get("P4")
	plane4.Update(airplane.WithLatitude(0), airplane.WithLongitude(0))

	// p5: Same distance and same update time as plane1, but higher ICAO
	list.Ensure("P5")
	plane5, _ := list.Get("P5")
	plane5.Update(airplane.WithLatitude(52.1), airplane.WithLongitude(13.1))

	now := time.Now()
	plane1.Update(airplane.WithLastUpdate(now))
	plane5.Update(airplane.WithLastUpdate(now))

	sorted := list.Sorted(receiverLat, receiverLon)

	if len(sorted) != 5 {
		t.Fatalf("expected 5 planes, got %d", len(sorted))
	}

	if sorted[0].ICAO != "P1" {
		t.Errorf("expected 1st plane to be P1, got %s", sorted[0].ICAO)
	}

	if sorted[1].ICAO != "P5" {
		t.Errorf("expected 2nd plane to be P5, got %s", sorted[1].ICAO)
	}

	if sorted[2].ICAO != "P3" {
		t.Errorf("expected 3rd plane to be P3, got %s", sorted[2].ICAO)
	}

	if sorted[3].ICAO != "P2" {
		t.Errorf("expected 4th plane to be P2, got %s", sorted[3].ICAO)
	}

	if sorted[4].ICAO != "P4" {
		t.Errorf("expected 5th plane to be P4, got %s", sorted[4].ICAO)
	}
}

// TestSortedTrailsOptOut locks the hot-path saving: the default Sorted
// pass drops position history from every snapshot (the plane list and
// stats consumers never read it), and WithTrails opts back in for the
// radar draw that does.
func TestSortedTrailsOptOut(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	list.Ensure("TRL1")

	plane, _ := list.Get("TRL1")
	plane.Update(airplane.WithPosition(52.1, 13.1))

	if got := len(plane.GetSnapshot().PositionHistory); got != 1 {
		t.Fatalf("setup: plane should hold 1 fix, got %d", got)
	}

	for _, snap := range list.Sorted(52.0, 13.0) {
		if snap.PositionHistory != nil {
			t.Errorf("default Sorted: PositionHistory = %v, want nil", snap.PositionHistory)
		}
	}

	withTrails := list.Sorted(52.0, 13.0, airplanes.WithTrails())
	if len(withTrails) != 1 {
		t.Fatalf("WithTrails Sorted: got %d snapshots, want 1", len(withTrails))
	}

	if got := len(withTrails[0].PositionHistory); got != 1 {
		t.Errorf("WithTrails Sorted: history len = %d, want 1 (trail retained)", got)
	}
}

func TestHaversineDistance_EdgeCases(t *testing.T) {
	t.Parallel()

	list := airplanes.New()
	list.Ensure("ORIGIN")
	plane, _ := list.Get("ORIGIN")
	plane.Update(airplane.WithLatitude(0), airplane.WithLongitude(0))

	_ = list.Sorted(0, 0)
}

func TestConcurrency(t *testing.T) {
	t.Parallel()

	list := airplanes.New()

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)

	worker := func() {
		defer waitGroup.Done()

		for range 1000 {
			list.Ensure("BUSY")
			list.Get("BUSY")
			list.Count()
			list.Sorted(52, 13)
			list.Prune(1 * time.Hour)
		}
	}
	go worker()
	go worker()

	waitGroup.Wait()
}

// BenchmarkSorted measures Sorted's per-call cost across a 200-plane
// fleet, with and without WithTrails (the option that additionally
// copies each snapshot's position history). Each plane here carries one
// real position fix rather than block 3a's full 256-entry trail:
// WithPosition's history append is gated behind a real 10s-per-fix
// interval kept on the private *airplane.Airplane, and that field is
// inaccessible from this package (block 3a's forcePositionAppend lives
// in pkg/airplane's own internal test file, which is off-limits across
// a package boundary). Sequential or concurrent, driving 256 real fixes
// through the public API costs ~256 * 10s regardless of plane count, so
// it is not viable in a benchmark that has to run quickly. One fix per
// plane still exercises the WithTrails() copy branch and the per-plane
// RLock/snapshot allocation Sorted's hot path is otherwise dominated by.
func BenchmarkSorted(b *testing.B) {
	const benchPlaneCount = 200

	list := airplanes.New()

	for index := range benchPlaneCount {
		icao := fmt.Sprintf("BN%04d", index)
		list.Ensure(icao)

		plane, _ := list.Get(icao)
		plane.Update(airplane.WithPosition(52.0+float64(index)*0.01, 13.0))
	}

	const receiverLat, receiverLon = 52.0, 13.0

	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			_ = list.Sorted(receiverLat, receiverLon)
		}
	})

	b.Run("WithTrails", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			_ = list.Sorted(receiverLat, receiverLon, airplanes.WithTrails())
		}
	})
}
