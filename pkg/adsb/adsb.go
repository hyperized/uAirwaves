// Package adsb owns the ADS-B ingest path. The historical
// implementation consumed readsb's JSON-over-TCP stream; this
// version drives the radio in-process via the
// github.com/hyperized/{rtl2832u,demod1090,modes} stack —
// USB → IQ samples → bit-level demodulation → typed Mode S
// messages — and aggregates the per-frame state into
// pkg/airplanes.
package adsb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"github.com/hyperized/rtl2832u"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

const (
	// readChunkSize is the IQ buffer the demodulator consumes
	// per Process call. 32 KiB matches one URB-ring iteration on
	// the rtl2832u Linux backend, so each chunk is the freshest
	// bytes the kernel just delivered.
	readChunkSize = 32 * 1024

	// cprPairWindow is the maximum age a cached CPR frame can
	// have when paired with a fresh opposite-format frame for a
	// globally-unambiguous decode. Per DO-260B §A.1.7.10 the
	// window is 10 seconds; we honour that strictly.
	cprPairWindow = 10 * time.Second

	// cprCacheCleanupInterval is how often the CPR cache
	// removes entries older than cprPairWindow. Cheap; runs on
	// the same goroutine as the demod loop.
	cprCacheCleanupInterval = 30 * time.Second

	defaultPruneThreshold = 1 * time.Minute
	defaultPruneFrequency = 5 * time.Second
)

// errOpenReceiver is the static sentinel for the "couldn't open
// the dongle" failure mode. err113 forbids ad-hoc errors.New
// from fmt.Errorf; static + %w keeps callers branchable.
var errOpenReceiver = errors.New("adsb: open RTL-SDR receiver")

// ADSB is the SDR-driven ADS-B ingest. The shape mirrors the
// original (TCP) implementation so main.go does not change:
// `adsb.New(opts...).Stream(ctx, planes)`.
type ADSB struct {
	pruneThreshold time.Duration
	pruneFrequency time.Duration

	// myLocation, when set, supplies the reference position for
	// locally-unambiguous CPR decoding — fast first-fix on every
	// position frame, no even/odd pairing wait. nil falls back
	// to globally-unambiguous decoding (waits ~10 s for the
	// matching CPR half).
	myLocation *location.Location

	// cpr caches the most recent even / odd half-position
	// per aircraft so a paired frame can resolve to lat/lon
	// when no reference position is available.
	cpr cprCache
}

// Option configures the ADSB stream.
type Option func(*ADSB)

// New returns an ADSB stream with the supplied options. Defaults
// to a 1-minute prune threshold and 5-second prune frequency.
func New(opts ...Option) *ADSB {
	stream := &ADSB{
		pruneThreshold: defaultPruneThreshold,
		pruneFrequency: defaultPruneFrequency,
		cpr:            newCPRCache(),
	}

	for _, opt := range opts {
		opt(stream)
	}

	return stream
}

// WithPruneFrequency sets how often stale aircraft are evicted
// from the live list.
func WithPruneFrequency(frequency time.Duration) Option {
	return func(a *ADSB) {
		if frequency > 0 {
			a.pruneFrequency = frequency
		}
	}
}

// WithPruneThreshold sets the staleness deadline an aircraft has
// to beat before it is evicted from the live list.
func WithPruneThreshold(threshold time.Duration) Option {
	return func(a *ADSB) {
		if threshold > 0 {
			a.pruneThreshold = threshold
		}
	}
}

// WithLocation supplies the receiver's position for
// locally-unambiguous CPR decoding. The location is read on
// every position frame, so a GPS-driven location that updates
// over time is fine — the decoder uses whatever the location
// reports at frame time. nil disables local decode and falls
// back to globally-unambiguous CPR pairing.
func WithLocation(loc *location.Location) Option {
	return func(a *ADSB) { a.myLocation = loc }
}

// Stream drives the SDR pipeline and updates planes as decoded
// frames arrive. Returns nil on context cancellation, an error
// wrapping errOpenReceiver if the dongle won't open.
func (a *ADSB) Stream(ctx context.Context, planes *airplanes.Airplanes) error {
	receiver, err := rtl2832u.Open(rtl2832u.WithAutoGain())
	if err != nil {
		return fmt.Errorf("%w: %w", errOpenReceiver, err)
	}

	defer func() {
		if cerr := receiver.Close(); cerr != nil {
			slog.Warn("adsb: receiver close", "error", cerr)
		}
	}()

	demodulator := demod.New(demod.WithSampleRate(rtl2832u.DefaultSampleRateHz))

	go a.prune(ctx, planes)
	go a.cpr.runCleanup(ctx, cprCacheCleanupInterval)

	iqBuf := make([]byte, readChunkSize)

	for {
		count, err := receiver.Read(ctx, iqBuf)

		if count > 0 {
			for _, frame := range demodulator.Process(iqBuf[:count]) {
				a.handleFrame(frame, planes)
			}
		}

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}

			return fmt.Errorf("adsb: read: %w", err)
		}
	}
}

// prune evicts stale aircraft on a fixed cadence until the
// context cancels.
func (a *ADSB) prune(ctx context.Context, planes *airplanes.Airplanes) {
	ticker := time.NewTicker(a.pruneFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			planes.Prune(a.pruneThreshold)
		}
	}
}

// cprCache holds the latest even / odd CPR half-position per
// ICAO so global decoding can pair them when the receiver has
// no fixed reference.
type cprCache struct {
	mu      sync.Mutex
	entries map[modes.ICAO]*cprEntry
}

type cprEntry struct {
	even       modes.CPRPosition
	odd        modes.CPRPosition
	evenSeenAt time.Time
	oddSeenAt  time.Time
	hasEven    bool
	hasOdd     bool
}

func newCPRCache() cprCache {
	return cprCache{entries: make(map[modes.ICAO]*cprEntry)}
}

// store records the latest CPR position for the aircraft and
// returns the resolved (lat, lon, ok) if the cache now holds a
// fresh pair of opposite formats.
//
//nolint:nonamedreturns // (lat, lon, ok) reads clearer named at this signature.
func (c *cprCache) store(icao modes.ICAO, pos modes.CPRPosition, now time.Time) (latitude, longitude float64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, found := c.entries[icao]
	if !found {
		entry = &cprEntry{}
		c.entries[icao] = entry
	}

	switch pos.Format {
	case modes.CPRFormatEven:
		entry.even = pos
		entry.evenSeenAt = now
		entry.hasEven = true
	case modes.CPRFormatOdd:
		entry.odd = pos
		entry.oddSeenAt = now
		entry.hasOdd = true
	default:
		return 0, 0, false
	}

	if !entry.hasEven || !entry.hasOdd {
		return 0, 0, false
	}

	mostRecent := modes.CPRFormatEven
	if entry.oddSeenAt.After(entry.evenSeenAt) {
		mostRecent = modes.CPRFormatOdd
	}

	older := entry.evenSeenAt
	if older.After(entry.oddSeenAt) {
		older = entry.oddSeenAt
	}

	if now.Sub(older) > cprPairWindow {
		return 0, 0, false
	}

	lat, lon, err := modes.DecodeCPRGlobal(entry.even, entry.odd, mostRecent)
	if err != nil {
		return 0, 0, false
	}

	return lat, lon, true
}

// runCleanup periodically evicts cache entries that have aged
// past 2× the pair window. Cheap O(N) walk.
func (c *cprCache) runCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.mu.Lock()

			for icao, entry := range c.entries {
				youngest := entry.evenSeenAt
				if entry.oddSeenAt.After(youngest) {
					youngest = entry.oddSeenAt
				}

				//nolint:mnd // 2× the pair window is the obvious cleanup horizon.
				if now.Sub(youngest) > 2*cprPairWindow {
					delete(c.entries, icao)
				}
			}

			c.mu.Unlock()
		}
	}
}

// resolveCPR returns the lat/lon for a position frame, using
// either the receiver's local reference (fast, single-frame) or
// the global pair-cache (slower, two-frame). Returns ok=false
// when neither path can resolve yet.
//
//nolint:nonamedreturns,revive // (lat, lon, ok) reads clearer named; one-character-over the line limit.
func (a *ADSB) resolveCPR(
	icao modes.ICAO, pos modes.CPRPosition, now time.Time,
) (latitude, longitude float64, ok bool) {
	if a.myLocation != nil {
		refLat, refLon := a.myLocation.GetCoordinates()
		if refLat != 0 || refLon != 0 {
			lat, lon := modes.DecodeCPRLocal(pos, refLat, refLon)

			return lat, lon, true
		}
	}

	return a.cpr.store(icao, pos, now)
}
