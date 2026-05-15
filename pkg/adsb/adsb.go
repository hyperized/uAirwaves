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
	"sync/atomic"
	"time"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/demod1090/icaofilter"
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

	// icaoTrustCleanupInterval is how often the icao filter
	// sweeps expired entries. Bounded enough to keep lock pressure
	// low, frequent enough that the trust set stays small under
	// heavy noise. The trust window itself defaults to one minute
	// (icaofilter.DefaultWindow), matching defaultPruneThreshold
	// so the trust list and the airplanes list age out together.
	icaoTrustCleanupInterval = 30 * time.Second

	// beastReconnectBaseDelay and beastReconnectMaxDelay mirror the
	// pkg/gps backoff schedule (1 s base, doubling, 30 s cap) so a
	// single behavioural envelope covers every TCP-backed source.
	beastReconnectBaseDelay = 1 * time.Second
	beastReconnectMaxDelay  = 30 * time.Second
)

// errOpenReceiver is the static sentinel for the "couldn't open
// the dongle" failure mode. err113 forbids ad-hoc errors.New
// from fmt.Errorf; static + %w keeps callers branchable.
var errOpenReceiver = errors.New("adsb: open RTL-SDR receiver")

// Receiver is the slice of *rtl2832u.Receiver pkg/adsb actually
// uses. Hoisted to an interface so Stream() can be tested with a
// fake or driven by a non-USB source (UAIRWAVES_REPLAY_IQ in main).
type Receiver interface {
	Read(ctx context.Context, p []byte) (int, error)
	Close() error
}

// ReceiverFactory builds a Receiver. Producing the receiver inside
// Stream (rather than passing one in) keeps the API consistent
// with rtl2832u's "open at use-site" pattern — the lifetime is
// scoped to a single Stream call so a re-call after a transient
// failure can re-open without the caller wiring up the lifecycle.
type ReceiverFactory func() (Receiver, error)

// Demodulator is the slice of *demod.Demodulator pkg/adsb uses.
// Same rationale as Receiver: an interface means the test path
// can substitute a fake that returns pre-canned frames.
type Demodulator interface {
	Process(samples []byte) []demod.Frame
}

// DemodulatorFactory builds a Demodulator. Same rationale as
// ReceiverFactory.
type DemodulatorFactory func() Demodulator

// ADSB is the SDR-driven ADS-B ingest. The shape mirrors the
// original (TCP) implementation so main.go does not change:
// `adsb.New(opts...).Stream(ctx, planes)`.
type ADSB struct {
	pruneThreshold time.Duration
	pruneFrequency time.Duration

	// receiverFactory and demodulatorFactory default to the
	// rtl2832u + demod stack. Tests inject fakes to drive Stream
	// without real silicon.
	receiverFactory    ReceiverFactory
	demodulatorFactory DemodulatorFactory

	// beastAddress, when non-empty, switches Stream away from
	// the SDR pipeline and into the BEAST-over-TCP path: dial,
	// read framed Mode S messages from a remote demodulator,
	// reconnect with backoff on failure.
	beastAddress string

	// beastDialer is the net.Dialer Stream uses for BEAST
	// connections. Tests override it via WithBeastDialer to point
	// at a loopback listener without going through the real
	// resolver.
	beastDialer BeastDialer

	// myLocation, when set, supplies the reference position for
	// locally-unambiguous CPR decoding — fast first-fix on every
	// position frame, no even/odd pairing wait. nil falls back
	// to globally-unambiguous decoding (waits ~10 s for the
	// matching CPR half).
	myLocation *location.Location

	// cpr caches the most recent even / odd half-position
	// per aircraft so a paired frame can resolve to lat/lon
	// when no reference position is available. Stored as a
	// pointer so the embedded sync.Mutex is never copied (e.g.
	// if ADSB itself is ever moved by value) — go vet copylocks
	// would otherwise flag the implicit address-take in
	// `go a.cpr.runCleanup(...)`.
	cpr *cprCache

	// icaoFilter is the address-parity phantom suppressor: only
	// admit family-B frames whose ICAO has been seen in a verified
	// family-A frame recently. See github.com/hyperized/demod1090/icaofilter.
	icaoFilter *icaofilter.Filter

	// totalFrames is every frame that came out of the demod
	// (clean + corrected). recoveredFrames counts the subset
	// the single-bit corrector rescued.
	// callsignsDecoded counts every IdentificationMessage that
	// the modes decoder produced (regardless of validity);
	// callsignsApplied counts the subset the validCallsign
	// filter let through. Their ratio surfaces noise pressure on
	// TC 1..4 frames in the stats panel.
	totalFrames      atomic.Uint64
	recoveredFrames  atomic.Uint64
	callsignsDecoded atomic.Uint64
	callsignsApplied atomic.Uint64

	// sourceLabel is the human-readable identifier the UI shows in
	// the header (e.g. "SDR", "BEAST 192.168.1.5:30005", "Replay
	// capture.iq"). Stamped once at construction via WithSourceLabel.
	sourceLabel string

	// connected reflects whether the current source is actively
	// producing frames: true after a successful SDR open / BEAST
	// connect, false during reconnect backoff or after the stream
	// exits. The BEAST consumer flips it per-attempt; the SDR
	// branch flips it once at open.
	connected atomic.Bool

	// bytesIn counts raw bytes pulled from the BEAST stream. SDR
	// and replay don't surface byte counters because samples are
	// the wrong unit and the rate is fixed by the demod chain.
	bytesIn atomic.Uint64
}

// Stats reports the ingest counters since process start.
type Stats struct {
	TotalFrames      uint64
	RecoveredFrames  uint64
	CallsignsDecoded uint64
	CallsignsApplied uint64
}

// Stats returns a snapshot of the ingest counters. Safe to call
// from any goroutine.
func (a *ADSB) Stats() Stats {
	return Stats{
		TotalFrames:      a.totalFrames.Load(),
		RecoveredFrames:  a.recoveredFrames.Load(),
		CallsignsDecoded: a.callsignsDecoded.Load(),
		CallsignsApplied: a.callsignsApplied.Load(),
	}
}

// SourceInfo describes the active ingest source for the UI
// header. Label is the human-readable identifier (e.g. "BEAST
// host:port", "SDR"). Connected mirrors the active-stream state;
// BytesIn is 0 for non-network sources.
type SourceInfo struct {
	Label     string
	Connected bool
	BytesIn   uint64
}

// Source returns a snapshot of the ingest source state. Safe to
// call from any goroutine.
func (a *ADSB) Source() SourceInfo {
	return SourceInfo{
		Label:     a.sourceLabel,
		Connected: a.connected.Load(),
		BytesIn:   a.bytesIn.Load(),
	}
}

// Option configures the ADSB stream.
type Option func(*ADSB)

// New returns an ADSB stream with the supplied options. Defaults
// to a 1-minute prune threshold and 5-second prune frequency, and
// the production rtl2832u + demod1090 stack for the receiver and
// demodulator.
func New(opts ...Option) *ADSB {
	stream := &ADSB{
		pruneThreshold:     defaultPruneThreshold,
		pruneFrequency:     defaultPruneFrequency,
		cpr:                newCPRCache(),
		icaoFilter:         icaofilter.New(),
		receiverFactory:    defaultReceiverFactory,
		demodulatorFactory: defaultDemodulatorFactory,
		beastDialer:        defaultBeastDialer,
	}

	for _, opt := range opts {
		opt(stream)
	}

	return stream
}

// defaultReceiverFactory opens a real RTL-SDR via the rtl2832u
// driver. rtl2832u's default config puts every R820T2 stage on
// the chip's internal AGC loops, with the demod's RF/IF AGC loop
// disabled (rtl2832u v0.1.3+ — earlier versions kept it on for
// SignalStats and the two AGCs fought, killing decode). Same
// config librtlsdr / readsb use for `--gain auto`.
//
//nolint:ireturn // factory: returning the interface is the seam tests rely on.
func defaultReceiverFactory() (Receiver, error) {
	rcv, err := rtl2832u.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errOpenReceiver, err)
	}

	return rcv, nil
}

// defaultDemodulatorFactory builds a demod1090 Demodulator at the
// rtl2832u stack's default sample rate (2.4 MS/s) with the two
// preamble / bit-recovery knobs that meaningfully improve real-
// world yield: permissive preamble matching plus single-bit
// error correction.
//
// Field measurement on a Jetvision antenna direct-feeding a
// uConsole-attached RTL-SDR (no LNA, auto-gain, 60 s capture):
// strict-default produces 7 DF 17 frames and 1 ident; permissive
// + error-correction produces 43 DF 17 and 7 idents — a 6× /7×
// jump. Closes the long-standing "I see flights but never IDs"
// gap relative to readsb's defaults.
//
//nolint:ireturn // factory: returning the interface is the seam tests rely on.
func defaultDemodulatorFactory() Demodulator {
	return demod.New(
		demod.WithSampleRate(rtl2832u.DefaultSampleRateHz),
		demod.WithPermissivePreamble(),
		demod.WithErrorCorrection(),
	)
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

// WithReceiverFactory replaces the default rtl2832u-backed
// receiver builder. The factory is invoked once per Stream call,
// so a re-entrant Stream after a transient failure produces a
// fresh receiver. Pass nil to clear and fall back to the default.
func WithReceiverFactory(factory ReceiverFactory) Option {
	return func(a *ADSB) {
		if factory != nil {
			a.receiverFactory = factory
		}
	}
}

// WithDemodulatorFactory replaces the default demod1090-backed
// demodulator builder. Same lifecycle as WithReceiverFactory:
// invoked once per Stream call. Pass nil to clear.
func WithDemodulatorFactory(factory DemodulatorFactory) Option {
	return func(a *ADSB) {
		if factory != nil {
			a.demodulatorFactory = factory
		}
	}
}

// WithBeastAddress switches Stream from the local SDR pipeline
// to a BEAST-over-TCP consumer: dial the given host:port, read
// framed Mode S messages from the remote demodulator, and feed
// them through the same handleFrame path the SDR loop uses.
//
// Empty string clears the address and falls back to the SDR
// pipeline. UAIRWAVES_REPLAY_IQ (set via WithReceiverFactory in
// main) takes precedence when both are wired — Stream consults
// beastAddress only when no replay factory has been configured.
func WithBeastAddress(address string) Option {
	return func(a *ADSB) { a.beastAddress = address }
}

// WithBeastDialer overrides the net.Dialer used to reach the
// BEAST server. Tests point this at a loopback listener so they
// don't depend on the system resolver or real hardware. nil
// keeps the default.
func WithBeastDialer(dialer BeastDialer) Option {
	return func(a *ADSB) {
		if dialer != nil {
			a.beastDialer = dialer
		}
	}
}

// WithSourceLabel sets the human-readable identifier the UI shows
// in the header (e.g. "SDR", "BEAST 192.168.1.5:30005", "Replay
// capture.iq"). Empty string leaves the label unset.
func WithSourceLabel(label string) Option {
	return func(a *ADSB) { a.sourceLabel = label }
}

// Stream drives the configured ingest source and updates planes
// as decoded frames arrive. Two paths:
//
//   - BEAST mode (WithBeastAddress set): dial a remote BEAST
//     server, read pre-demodulated Mode S frames, and feed them
//     through handleFrame. Reconnects with exponential backoff.
//   - SDR mode (default): drive the receiver + demodulator
//     factories, processing IQ chunks into frames.
//
// Both paths share the prune + CPR-cleanup goroutines started
// here, so callers don't see different lifecycle semantics across
// modes.
//
// Returns:
//
//   - nil on context cancellation (user pressed q / SIGINT) or on
//     ErrReplayEnded (the file-backed receiver exhausted its
//     capture). Both are clean shutdowns from the UI's perspective.
//   - an error from the configured ReceiverFactory if the SDR
//     source can't be opened. The default factory wraps
//     errOpenReceiver for SDR-open failures; NewFileReceiver wraps
//     errOpenReplay for missing replay files.
//   - a wrapped read error for anything else.
func (a *ADSB) Stream(ctx context.Context, planes *airplanes.Airplanes) error {
	go a.prune(ctx, planes)
	go a.cpr.runCleanup(ctx, cprCacheCleanupInterval)
	go a.icaoFilter.RunCleanup(ctx, icaoTrustCleanupInterval)

	if a.beastAddress != "" {
		return a.streamBeast(ctx, planes)
	}

	return a.streamSDR(ctx, planes)
}

// streamSDR runs the historical receiver+demodulator loop. Held
// in its own method so Stream can pick between SDR and BEAST
// without an inline branch obscuring the read-loop shape.
func (a *ADSB) streamSDR(ctx context.Context, planes *airplanes.Airplanes) error {
	receiver, err := a.receiverFactory()
	if err != nil {
		return err
	}

	a.connected.Store(true)

	defer func() {
		a.connected.Store(false)

		if cerr := receiver.Close(); cerr != nil {
			slog.Warn("adsb: receiver close", "error", cerr)
		}
	}()

	demodulator := a.demodulatorFactory()
	iqBuf := make([]byte, readChunkSize)

	for {
		count, err := receiver.Read(ctx, iqBuf)

		if count > 0 {
			for _, frame := range demodulator.Process(iqBuf[:count]) {
				a.handleFrame(frame, planes)
			}
		}

		if err != nil {
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, ErrReplayEnded) {
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

func newCPRCache() *cprCache {
	return &cprCache{entries: make(map[modes.ICAO]*cprEntry)}
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
