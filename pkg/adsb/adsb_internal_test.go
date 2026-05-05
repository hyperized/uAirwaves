package adsb

import (
	"testing"
	"time"

	"github.com/hyperized/modes"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	stream := New()

	if stream.pruneFrequency != defaultPruneFrequency {
		t.Errorf("pruneFrequency = %v, want %v", stream.pruneFrequency, defaultPruneFrequency)
	}

	if stream.pruneThreshold != defaultPruneThreshold {
		t.Errorf("pruneThreshold = %v, want %v", stream.pruneThreshold, defaultPruneThreshold)
	}

	if stream.myLocation != nil {
		t.Errorf("myLocation = %v, want nil", stream.myLocation)
	}
}

func TestWithPruneFrequency(t *testing.T) {
	t.Parallel()

	stream := New(WithPruneFrequency(2 * time.Second))
	if stream.pruneFrequency != 2*time.Second {
		t.Errorf("pruneFrequency = %v, want 2s", stream.pruneFrequency)
	}

	// Zero / negative inputs are ignored.
	stream = New(WithPruneFrequency(0))
	if stream.pruneFrequency != defaultPruneFrequency {
		t.Errorf("zero ignored: pruneFrequency = %v, want default", stream.pruneFrequency)
	}
}

func TestWithPruneThreshold(t *testing.T) {
	t.Parallel()

	stream := New(WithPruneThreshold(30 * time.Second))
	if stream.pruneThreshold != 30*time.Second {
		t.Errorf("pruneThreshold = %v, want 30s", stream.pruneThreshold)
	}
}

func TestCPRCachePairsEvenAndOdd(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	now := time.Now()

	// Hand-derived raw values that decode to a sane (lat, lon)
	// pair under the global algorithm. Reuse the same pair the
	// modes package round-trip-tests against — no decode value
	// pinned here, only the "ok=true" outcome we need.
	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}
	odd := modes.CPRPosition{Latitude: 88385, Longitude: 125818, Format: modes.CPRFormatOdd}

	const icao modes.ICAO = 0x484755

	if _, _, ok := cache.store(icao, even, now); ok {
		t.Error("first store (even only) should not resolve")
	}

	if _, _, ok := cache.store(icao, odd, now.Add(time.Second)); !ok {
		t.Error("paired even+odd should resolve")
	}
}

func TestCPRCacheRejectsStalePair(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	now := time.Now()

	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}
	odd := modes.CPRPosition{Latitude: 88385, Longitude: 125818, Format: modes.CPRFormatOdd}

	const icao modes.ICAO = 0x484755

	cache.store(icao, even, now)
	// Even is far in the past; pairing must reject.
	tooLate := now.Add(cprPairWindow + time.Second)

	if _, _, ok := cache.store(icao, odd, tooLate); ok {
		t.Error("stale pair should not resolve")
	}
}
