package coverage_test

import (
	"sync"
	"testing"

	"github.com/hyperized/uAirwaves/pkg/coverage"
)

func TestObserveAndSnapshot(t *testing.T) {
	t.Parallel()

	const (
		distanceNm = 15.0
		bearingDeg = 100.0
		altFt      = 6000.0
		wantBin    = 1
		wantBand   = 1
		wantSector = 4
	)

	tracker := coverage.New()
	tracker.Observe(distanceNm, bearingDeg, altFt)

	snapshot := tracker.Snapshot()

	if snapshot.Cells[wantBand][wantBin] != 1 {
		t.Errorf("Cells[%d][%d] = %d, want 1", wantBand, wantBin, snapshot.Cells[wantBand][wantBin])
	}

	if snapshot.Sectors[wantSector] != distanceNm {
		t.Errorf("Sectors[%d] = %v, want %v", wantSector, snapshot.Sectors[wantSector], distanceNm)
	}

	if snapshot.MaxRangeNm != distanceNm {
		t.Errorf("MaxRangeNm = %v, want %v", snapshot.MaxRangeNm, distanceNm)
	}

	if snapshot.MaxRangeAltFt != altFt {
		t.Errorf("MaxRangeAltFt = %v, want %v", snapshot.MaxRangeAltFt, altFt)
	}
}

func TestBearingNormalizationEndToEnd(t *testing.T) {
	t.Parallel()

	t.Run("negative bearing matches positive equivalent", func(t *testing.T) {
		t.Parallel()

		const (
			distanceNm = 50.0
			altFt      = 1000.0
			negative   = -45.0
			equivalent = 315.0
		)

		negativeTracker := coverage.New()
		negativeTracker.Observe(distanceNm, negative, altFt)

		equivalentTracker := coverage.New()
		equivalentTracker.Observe(distanceNm, equivalent, altFt)

		negativeSectors := negativeTracker.Snapshot().Sectors
		equivalentSectors := equivalentTracker.Snapshot().Sectors

		if negativeSectors != equivalentSectors {
			t.Errorf("Sectors for bearing %v = %v, want match for bearing %v = %v",
				negative, negativeSectors, equivalent, equivalentSectors)
		}
	})

	t.Run("bearing beyond full circle matches equivalent", func(t *testing.T) {
		t.Parallel()

		const (
			distanceNm = 50.0
			altFt      = 1000.0
			overflow   = 405.0
			equivalent = 45.0
		)

		overflowTracker := coverage.New()
		overflowTracker.Observe(distanceNm, overflow, altFt)

		equivalentTracker := coverage.New()
		equivalentTracker.Observe(distanceNm, equivalent, altFt)

		overflowSectors := overflowTracker.Snapshot().Sectors
		equivalentSectors := equivalentTracker.Snapshot().Sectors

		if overflowSectors != equivalentSectors {
			t.Errorf("Sectors for bearing %v = %v, want match for bearing %v = %v",
				overflow, overflowSectors, equivalent, equivalentSectors)
		}
	})
}

func TestSectorMaxRangeOnlyGrows(t *testing.T) {
	t.Parallel()

	const (
		bearingDeg = 100.0
		altFt      = 1000.0
		fartherNm  = 50.0
		closerNm   = 20.0
	)

	tracker := coverage.New()
	tracker.Observe(fartherNm, bearingDeg, altFt)

	afterFar := tracker.Snapshot()

	tracker.Observe(closerNm, bearingDeg, altFt)

	afterClose := tracker.Snapshot()

	if afterClose.Sectors != afterFar.Sectors {
		t.Errorf("Sectors after closer Observe = %v, want unchanged from %v", afterClose.Sectors, afterFar.Sectors)
	}
}

func TestGlobalMaxRangeKeepsFarthestFix(t *testing.T) {
	t.Parallel()

	const (
		farNm       = 100.0
		farBearing  = 30.0
		farAlt      = 8000.0
		nearNm      = 40.0
		nearBearing = 200.0
		nearAlt     = 20000.0
	)

	tracker := coverage.New()
	tracker.Observe(farNm, farBearing, farAlt)
	tracker.Observe(nearNm, nearBearing, nearAlt)

	snapshot := tracker.Snapshot()

	if snapshot.MaxRangeNm != farNm {
		t.Errorf("MaxRangeNm = %v, want %v", snapshot.MaxRangeNm, farNm)
	}

	if snapshot.MaxRangeAltFt != farAlt {
		t.Errorf("MaxRangeAltFt = %v, want %v", snapshot.MaxRangeAltFt, farAlt)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	t.Parallel()

	const (
		firstNm       = 5.0
		firstBearing  = 10.0
		firstAlt      = 100.0
		secondNm      = 8.0
		secondBearing = 280.0
		secondAlt     = 200.0
		wantBin       = 0
		wantBand      = 0
	)

	tracker := coverage.New()
	tracker.Observe(firstNm, firstBearing, firstAlt)

	before := tracker.Snapshot()

	tracker.Observe(secondNm, secondBearing, secondAlt)

	if before.MaxRangeNm != firstNm {
		t.Errorf("earlier snapshot MaxRangeNm = %v, want %v (must not see later Observe)", before.MaxRangeNm, firstNm)
	}

	if before.MaxRangeAltFt != firstAlt {
		t.Errorf("earlier snapshot MaxRangeAltFt = %v, want %v", before.MaxRangeAltFt, firstAlt)
	}

	if before.Cells[wantBand][wantBin] != 1 {
		t.Errorf("earlier snapshot Cells[%d][%d] = %d, want 1 (must not see later Observe)",
			wantBand, wantBin, before.Cells[wantBand][wantBin])
	}
}

func TestConcurrentObserve(t *testing.T) {
	t.Parallel()

	const (
		goroutineCount     = 8
		observesPerRoutine = 200
		distanceNm         = 42.0
		bearingDeg         = 123.0
		altFt              = 3000.0
		want               = goroutineCount * observesPerRoutine
	)

	tracker := coverage.New()

	var waitGroup sync.WaitGroup

	waitGroup.Add(goroutineCount)

	for range goroutineCount {
		go func() {
			defer waitGroup.Done()

			for range observesPerRoutine {
				tracker.Observe(distanceNm, bearingDeg, altFt)
			}
		}()
	}

	waitGroup.Wait()

	snapshot := tracker.Snapshot()

	var total uint32

	for band := range snapshot.Cells {
		for bin := range snapshot.Cells[band] {
			total += snapshot.Cells[band][bin]
		}
	}

	if total != want {
		t.Errorf("total cell count = %d, want %d", total, want)
	}
}
