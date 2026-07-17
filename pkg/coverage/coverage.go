// Package coverage accumulates an antenna's observed ADS-B reception
// pattern into fixed-size distance, altitude and bearing bins. It has no
// UI dependencies; other packages read the exported constants to size axis
// labels and render the grids.
package coverage

import (
	"math"
	"sync"
)

const (
	// DistanceBinNm is the number of nautical miles spanned by one
	// distance bin.
	DistanceBinNm = 10.0
	// DistanceBinCount is the number of distance bins. Bins 0..24 span
	// 0..250 nm; distances at or beyond 250 nm clamp into bin 24.
	DistanceBinCount = 25
	// AltitudeBandFt is the number of feet spanned by one altitude band.
	AltitudeBandFt = 5000.0
	// AltitudeBandCount is the number of altitude bands. Bands 0..9 span
	// 0..50000 ft; altitudes at or beyond 50000 ft clamp into band 9.
	AltitudeBandCount = 10
	// BearingSectorCount is the number of bearing sectors. 16 sectors
	// each span 22.5 degrees of compass bearing.
	BearingSectorCount = 16
)

const (
	// fullCircleDeg is the number of degrees in a full compass circle.
	fullCircleDeg = 360.0
	// sectorWidthDeg is the angular width of one bearing sector.
	sectorWidthDeg = fullCircleDeg / BearingSectorCount
)

// Snapshot is a value copy of the accumulated coverage state, safe to read
// without holding the Tracker's lock. Cells is indexed
// [altitudeBand][distanceBin]; band 0 is the lowest altitude (0-5000 ft),
// distance bin 0 the nearest range ring (0-10 nm). Arrays are value types,
// so copying a Snapshot deep-copies the grids: mutating the Tracker after
// Snapshot returns does not affect a previously returned Snapshot.
type Snapshot struct {
	Cells         [AltitudeBandCount][DistanceBinCount]uint32
	Sectors       [BearingSectorCount]float64
	MaxRangeNm    float64
	MaxRangeAltFt float64
}

// Tracker accumulates CPR-decoded aircraft fixes into fixed-size bins so
// memory is constant regardless of traffic volume. It is safe for
// concurrent use: Observe is called from the ADS-B ingest goroutine while
// Snapshot is read from the render path. All state is guarded by a single
// mutex.
type Tracker struct {
	mu            sync.Mutex
	cells         [AltitudeBandCount][DistanceBinCount]uint32
	sectors       [BearingSectorCount]float64
	maxRangeNm    float64
	maxRangeAltFt float64
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{}
}

// Observe folds one aircraft fix into the coverage grids. distanceNm is the
// ground distance from the receiver, bearingDeg the compass bearing from
// the receiver (normalised internally into [0,360)), altFt the barometric
// altitude. It is called on the ADS-B ingest goroutine, so it must stay
// cheap: a single mutex, no allocation. Negative distance or altitude are
// sentinel/invalid values and are ignored.
func (t *Tracker) Observe(distanceNm, bearingDeg, altFt float64) {
	if distanceNm < 0 || altFt < 0 {
		return
	}

	bearing := normalizeBearing(bearingDeg)
	sector := bearingSector(bearing)

	t.mu.Lock()
	saturatingInc(&t.cells[altitudeBand(altFt)][distanceBin(distanceNm)])

	if distanceNm > t.sectors[sector] {
		t.sectors[sector] = distanceNm
	}

	if distanceNm > t.maxRangeNm {
		t.maxRangeNm = distanceNm
		t.maxRangeAltFt = altFt
	}
	t.mu.Unlock()
}

// Snapshot returns a value copy of the current coverage state under the
// tracker's lock. The returned Snapshot is independent of further Observe
// calls.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	snapshot := Snapshot{
		Cells:         t.cells,
		Sectors:       t.sectors,
		MaxRangeNm:    t.maxRangeNm,
		MaxRangeAltFt: t.maxRangeAltFt,
	}
	t.mu.Unlock()

	return snapshot
}

// distanceBin maps a ground distance in nautical miles to its bin index,
// clamping anything at or beyond DistanceBinCount*DistanceBinNm into the
// last bin.
func distanceBin(distanceNm float64) int {
	bin := int(distanceNm / DistanceBinNm)
	if bin > DistanceBinCount-1 {
		return DistanceBinCount - 1
	}

	return bin
}

// altitudeBand maps a barometric altitude in feet to its band index,
// clamping anything at or beyond AltitudeBandCount*AltitudeBandFt into the
// top band.
func altitudeBand(altFt float64) int {
	band := int(altFt / AltitudeBandFt)
	if band > AltitudeBandCount-1 {
		return AltitudeBandCount - 1
	}

	return band
}

// bearingSector maps an already-normalised bearing in [0,360) to its sector
// index, clamping the 360-boundary floating-point edge into the last
// sector.
func bearingSector(bearingDeg float64) int {
	sector := int(bearingDeg / sectorWidthDeg)
	if sector > BearingSectorCount-1 {
		return BearingSectorCount - 1
	}

	return sector
}

// normalizeBearing wraps any bearing into [0,360).
func normalizeBearing(deg float64) float64 {
	normalized := math.Mod(deg, fullCircleDeg)
	if normalized < 0 {
		normalized += fullCircleDeg
	}

	return normalized
}

// saturatingInc increments the counter unless it is already at
// math.MaxUint32.
func saturatingInc(counter *uint32) {
	if *counter < math.MaxUint32 {
		*counter++
	}
}
