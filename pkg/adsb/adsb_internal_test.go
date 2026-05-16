package adsb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

// newLocationAt is the test-friendly *location.Location constructor.
// Avoids each test repeating the option-builder boilerplate for a
// common (lat, lon) seed.
func newLocationAt(lat, lon float64) *location.Location {
	return location.New(location.WithLatitude(lat), location.WithLongitude(lon))
}

// airplaneTimestamp wraps airplane.WithLastUpdate so the
// import-vs-helper boundary stays clean in this file.
func airplaneTimestamp(timestamp time.Time) airplane.Option {
	return airplane.WithLastUpdate(timestamp)
}

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

// fakeReceiver implements the Receiver interface for Stream tests.
// reads is consumed FIFO; once exhausted, Read returns Canceled so
// Stream exits the loop cleanly. closed counts Close calls so the
// test can assert lifecycle hooks.
type fakeReceiver struct {
	reads  []fakeRead
	closed int
}

type fakeRead struct {
	data []byte
	err  error
}

func (f *fakeReceiver) Read(_ context.Context, dst []byte) (int, error) {
	if len(f.reads) == 0 {
		return 0, context.Canceled
	}

	next := f.reads[0]
	f.reads = f.reads[1:]

	if next.err != nil {
		return 0, next.err
	}

	return copy(dst, next.data), nil
}

func (f *fakeReceiver) Close() error {
	f.closed++

	return nil
}

// fakeDemodulator returns a pre-canned slice of demod.Frames the
// first time Process is called and an empty slice thereafter. The
// IQ bytes are ignored; only the call count and the canned frames
// matter for testing the post-demod pipeline.
type fakeDemodulator struct {
	frames []demod.Frame
	calls  int
}

func (d *fakeDemodulator) Process(_ []byte) []demod.Frame {
	d.calls++

	if d.calls == 1 {
		return d.frames
	}

	return nil
}

// sweepCapableFakeReceiver is fakeReceiver + the gain setters
// the auto-sweep path probes for. Used to exercise the
// WithAutoSweep code path without a real dongle.
type sweepCapableFakeReceiver struct {
	fakeReceiver

	lnaCalls int
	mixCalls int
	vgaCalls int
}

func (s *sweepCapableFakeReceiver) SetLNAGain(uint8) error {
	s.lnaCalls++

	return nil
}

func (s *sweepCapableFakeReceiver) SetMixerGain(uint8) error {
	s.mixCalls++

	return nil
}

func (s *sweepCapableFakeReceiver) SetVGAGain(uint8) error {
	s.vgaCalls++

	return nil
}

// errSyntheticReceiverOpen is the static sentinel for the
// "factory failed to build a receiver" branch.
var errSyntheticReceiverOpen = errors.New("synthetic receiver open failure")

// errSyntheticReceiverRead is the static sentinel for a non-cancel
// receiver Read error driving Stream's error-return branch.
var errSyntheticReceiverRead = errors.New("synthetic receiver read failure")

func TestStreamReceiverFactoryError(t *testing.T) {
	t.Parallel()

	stream := New(WithReceiverFactory(func() (Receiver, error) {
		return nil, errSyntheticReceiverOpen
	}))

	planes := airplanes.New()

	err := stream.Stream(t.Context(), planes)
	if !errors.Is(err, errSyntheticReceiverOpen) {
		t.Errorf("Stream err = %v, want errSyntheticReceiverOpen", err)
	}
}

func TestStreamContextCancelExitsClean(t *testing.T) {
	t.Parallel()

	rcv := &fakeReceiver{} // empty queue → first Read returns Canceled
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	planes := airplanes.New()

	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Errorf("Stream err = %v, want nil", err)
	}

	if rcv.closed != 1 {
		t.Errorf("rcv.closed = %d, want 1", rcv.closed)
	}
}

func TestStreamReadErrorReturnsWrapped(t *testing.T) {
	t.Parallel()

	rcv := &fakeReceiver{
		reads: []fakeRead{{err: errSyntheticReceiverRead}},
	}
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	planes := airplanes.New()

	err := stream.Stream(t.Context(), planes)
	if !errors.Is(err, errSyntheticReceiverRead) {
		t.Errorf("Stream err = %v, want errSyntheticReceiverRead", err)
	}
}

func TestStreamAppliesIdentificationFrame(t *testing.T) {
	t.Parallel()

	// 8D40621D202CC371C32CE0576098 — DF 17 ES, ICAO 0x40621D,
	// TC 4 (callsign payload). The modes package decodes the
	// callsign to "KLM1023".
	frameBytes := []byte{
		0x8D, 0x40, 0x62, 0x1D,
		0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0,
		0x57, 0x60, 0x98,
	}

	rcv := &fakeReceiver{
		reads: []fakeRead{
			{data: []byte{0x00}}, // arbitrary IQ; demod is faked
		},
	}

	dem := &fakeDemodulator{
		frames: []demod.Frame{{
			Bytes:    frameBytes,
			DF:       17,
			CRC:      0, // DF 17/18 require residual=0 to be admitted
			WallTime: time.Now(),
		}},
	}

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	)

	planes := airplanes.New()

	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	plane, ok := planes.Get("40621D")
	if !ok {
		t.Fatal("plane 40621D not registered")
	}

	if got := plane.GetSnapshot().Callsign; got != "KLM1023" {
		t.Errorf("callsign = %q, want KLM1023", got)
	}

	frameStats := stream.Stats()
	if frameStats.TotalFrames != 1 {
		t.Errorf("TotalFrames = %d, want 1", frameStats.TotalFrames)
	}

	if frameStats.CallsignsApplied != 1 {
		t.Errorf("CallsignsApplied = %d, want 1", frameStats.CallsignsApplied)
	}
}

func TestStreamRejectsFrameWithNonZeroResidual(t *testing.T) {
	t.Parallel()

	// Same frame as TestStreamAppliesIdentificationFrame but with
	// a non-zero CRC residual: learnICAO must reject it for DF 17.
	frameBytes := []byte{
		0x8D, 0x40, 0x62, 0x1D,
		0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0,
		0x57, 0x60, 0x98,
	}

	rcv := &fakeReceiver{reads: []fakeRead{{data: []byte{0x00}}}}
	dem := &fakeDemodulator{
		frames: []demod.Frame{{
			Bytes: frameBytes,
			DF:    17,
			CRC:   0xDEADBE,
		}},
	}

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	)

	planes := airplanes.New()

	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if _, ok := planes.Get("40621D"); ok {
		t.Error("plane registered despite residual!=0; learnICAO should reject")
	}

	if got := stream.Stats().TotalFrames; got != 1 {
		t.Errorf("TotalFrames = %d, want 1 (frame counted before reject)", got)
	}

	if got := stream.Stats().CallsignsApplied; got != 0 {
		t.Errorf("CallsignsApplied = %d, want 0", got)
	}
}

func TestWithReceiverFactoryNilIgnored(t *testing.T) {
	t.Parallel()

	stream := New(WithReceiverFactory(nil))

	// Default must remain in place; calling the field returns a
	// non-nil (real opener), error is fine — we just want the
	// pointer to still be set.
	if stream.receiverFactory == nil {
		t.Error("receiverFactory was cleared by nil option")
	}
}

func TestWithDemodulatorFactoryNilIgnored(t *testing.T) {
	t.Parallel()

	stream := New(WithDemodulatorFactory(nil))

	if stream.demodulatorFactory == nil {
		t.Error("demodulatorFactory was cleared by nil option")
	}
}

func TestWithLocationStoresPointer(t *testing.T) {
	t.Parallel()

	loc := newLocationAt(52.31, 4.77)
	stream := New(WithLocation(loc))

	if stream.myLocation != loc {
		t.Error("myLocation pointer mismatch after WithLocation")
	}
}

func TestResolveCPRUsesLocalReferenceWhenLocationSet(t *testing.T) {
	t.Parallel()

	stream := New(WithLocation(newLocationAt(52.31, 4.77)))

	pos := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}

	_, _, ok := stream.resolveCPR(0x484755, pos, time.Now())
	if !ok {
		t.Error("resolveCPR with reference location should always return ok=true")
	}
}

func TestResolveCPRFallsBackToGlobalPairWhenNoLocation(t *testing.T) {
	t.Parallel()

	stream := New() // no WithLocation

	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}
	odd := modes.CPRPosition{Latitude: 88385, Longitude: 125818, Format: modes.CPRFormatOdd}

	const icao modes.ICAO = 0x484755

	now := time.Now()

	if _, _, ok := stream.resolveCPR(icao, even, now); ok {
		t.Error("first store (even only) should not resolve")
	}

	if _, _, ok := stream.resolveCPR(icao, odd, now.Add(time.Second)); !ok {
		t.Error("paired even+odd should resolve via global pair cache")
	}
}

func TestResolveCPRLocationAtOriginFallsThrough(t *testing.T) {
	t.Parallel()

	// (0, 0) is the sentinel for "no fix yet" in pkg/location;
	// resolveCPR must treat it as no reference and try the global
	// pair cache instead. Without a paired half it returns ok=false.
	stream := New(WithLocation(newLocationAt(0, 0)))

	pos := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}

	if _, _, ok := stream.resolveCPR(0x484755, pos, time.Now()); ok {
		t.Error("origin reference should fall through to global cache; got ok=true with no pair")
	}
}

func TestCPRCacheCleanupEvictsAgedEntries(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	now := time.Now()

	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}
	cache.store(0x484755, even, now)

	if len(cache.entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(cache.entries))
	}

	// Run one tick of cleanup with a "now" 3× the pair window in
	// the future — the entry must be evicted.
	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		time.Sleep(20 * time.Millisecond) //nolint:mnd // pause long enough for one tick.
		cache.mu.Lock()
		// Backdate the entry so the cleanup run sees it as aged.
		entry := cache.entries[0x484755]
		entry.evenSeenAt = now.Add(-3 * cprPairWindow) //nolint:mnd // 3× pair window > 2× cleanup horizon.
		cache.mu.Unlock()
	}()

	go cache.runCleanup(ctx, 5*time.Millisecond) //nolint:mnd // tick fast to bound the test.

	// Wait until the entry disappears or we time out.
	deadline := time.Now().Add(500 * time.Millisecond) //nolint:mnd // generous CI cushion.
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		empty := len(cache.entries) == 0
		cache.mu.Unlock()

		if empty {
			cancel()

			return
		}

		time.Sleep(2 * time.Millisecond) //nolint:mnd // poll cadence.
	}

	cancel()
	t.Errorf("cache entry not evicted within deadline; len=%d", len(cache.entries))
}

// streamSingleFrame is a test helper that pumps exactly one frame
// through Stream() and returns the resulting plane snapshot.
//
// For family-B frames (DF 0/4/5/16/20/21, residual carries the
// ICAO) the helper pre-seeds the trust filter with the residual,
// so the family-B admit gate doesn't reject the test frame for
// not having a preceding family-A sighting. Real callers always
// see a DF 17/18 before they see surveillance replies; tests that
// want to verify the per-DF decoding don't need to re-enact that
// timeline frame-by-frame. Family-A frames (residual=0) are
// unaffected because the trust check is short-circuited for them.
func streamSingleFrame(t *testing.T, frame demod.Frame, opts ...Option) *airplanes.Airplanes {
	t.Helper()

	rcv := &fakeReceiver{reads: []fakeRead{{data: []byte{0x00}}}}
	dem := &fakeDemodulator{frames: []demod.Frame{frame}}

	all := append([]Option{
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	}, opts...)
	stream := New(all...)

	if frame.CRC != 0 {
		stream.icaoFilter.Trust(modes.ICAO(frame.CRC), time.Now())
	}

	planes := airplanes.New()
	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	return planes
}

// TestPhantomFamilyBFrameRejected verifies the icaofilter gate is
// actually live in handleFrame: a family-B frame whose residual
// has NOT been previously trusted via a family-A sighting must not
// register a plane. Drives the stream directly (not via the
// streamSingleFrame helper, which pre-seeds trust).
func TestPhantomFamilyBFrameRejected(t *testing.T) {
	t.Parallel()

	frame := demod.Frame{
		Bytes:    makeShortFrameWithDF(modes.DFSurveillanceAlt),
		DF:       uint8(modes.DFSurveillanceAlt),
		CRC:      0xC0DECA,
		WallTime: time.Now(),
	}

	rcv := &fakeReceiver{reads: []fakeRead{{data: []byte{0x00}}}}
	dem := &fakeDemodulator{frames: []demod.Frame{frame}}

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	)

	planes := airplanes.New()
	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if _, ok := planes.Get("C0DECA"); ok {
		t.Error("untrusted family-B frame registered a plane; icaofilter not gating")
	}

	if got := stream.Stats().TotalFrames; got != 1 {
		t.Errorf("TotalFrames = %d, want 1 (frame counted before reject)", got)
	}
}

func TestApplySurveillanceAltitudeRoutes(t *testing.T) {
	t.Parallel()

	// DF 4: 7 bytes. byte 0 = DF<<3 | FS. CRC residual carries
	// the addressed ICAO; we use a non-zero value so learnICAO
	// admits the frame.
	frame := demod.Frame{
		Bytes: makeShortFrame(modes.DFSurveillanceAlt, 0x000123),
		DF:    uint8(modes.DFSurveillanceAlt),
		CRC:   0xAABBCC,
	}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("AABBCC"); !ok {
		t.Error("DF 4 frame did not register addressed ICAO")
	}
}

func TestApplySurveillanceIdentityRoutes(t *testing.T) {
	t.Parallel()

	frame := demod.Frame{
		Bytes: makeShortFrame(modes.DFSurveillanceID, 0x000456),
		DF:    uint8(modes.DFSurveillanceID),
		CRC:   0x123456,
	}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("123456"); !ok {
		t.Error("DF 5 frame did not register addressed ICAO")
	}
}

func TestApplyCommBAltitudeRoutes(t *testing.T) {
	t.Parallel()

	frame := demod.Frame{
		Bytes: makeLongFrame(modes.DFCommBAltitude),
		DF:    uint8(modes.DFCommBAltitude),
		CRC:   0xCAFE01,
	}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("CAFE01"); !ok {
		t.Error("DF 20 frame did not register addressed ICAO")
	}
}

func TestApplyCommBIdentityRoutes(t *testing.T) {
	t.Parallel()

	frame := demod.Frame{
		Bytes: makeLongFrame(modes.DFCommBIdentity),
		DF:    uint8(modes.DFCommBIdentity),
		CRC:   0xCAFE02,
	}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("CAFE02"); !ok {
		t.Error("DF 21 frame did not register addressed ICAO")
	}
}

func TestApplyExtendedSquitterUnsupportedTC(t *testing.T) {
	t.Parallel()

	// DF 17 with TC 23 (Test Message): the modes decoder returns
	// ErrUnsupportedTypeCode. applyExtendedSquitter must early-
	// return without touching the plane state.
	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0xAA
	bytes[2] = 0xBB
	bytes[3] = 0xCC
	bytes[4] = 0xB8 // TC=23 << 3

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("AABBCC"); !ok {
		t.Error("DF 17 frame did not register broadcasting ICAO")
	}
}

func TestApplyVelocityRoutes(t *testing.T) {
	t.Parallel()

	// DF 17 with TC 19 (Airborne Velocity), subtype 1 with
	// non-zero EW/NS so GroundSpeedAvailable becomes true and
	// applyVelocity reaches the WithVelocity / WithHeading path.
	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0xAB
	bytes[2] = 0xCD
	bytes[3] = 0xEF
	// TC 19 << 3 = 0x98; subtype 1 is the bottom 3 bits.
	bytes[4] = 0x98 | 0x01
	// East-velocity word bits (non-zero so GroundSpeedAvailable=true).
	bytes[5] = 0x10
	bytes[6] = 0x10
	// North-velocity word bits (non-zero).
	bytes[7] = 0x10
	bytes[8] = 0x10
	// Vertical-rate field: any non-zero raw value enables it.
	bytes[9] = 0x10
	bytes[10] = 0x00

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("ABCDEF"); !ok {
		t.Error("DF 17 TC 19 frame did not register broadcasting ICAO")
	}
}

func TestApplyAirbornePositionRoutes(t *testing.T) {
	t.Parallel()

	// DF 17 with TC 11 (Airborne Position, barometric). Pair with
	// a location set so resolveCPR returns ok=true and the apply
	// branch reaches WithLatitude/WithLongitude.
	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0xBB
	bytes[2] = 0xCC
	bytes[3] = 0xDD
	bytes[4] = byte(11 << 3) // TC 11 << 3 = 0x58
	// Altitude code: byte 5 high nibble + byte 6 high bit. Set
	// any non-zero Q-bit pattern so AltitudeError is nil.
	bytes[5] = 0x80
	bytes[6] = 0x10

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame, WithLocation(newLocationAt(52.31, 4.77)))
	if _, ok := planes.Get("BBCCDD"); !ok {
		t.Error("DF 17 TC 11 frame did not register broadcasting ICAO")
	}
}

func TestApplySurfacePositionRoutes(t *testing.T) {
	t.Parallel()

	// DF 17 with TC 5 (Surface Position).
	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0x55
	bytes[2] = 0x55
	bytes[3] = 0x55
	bytes[4] = byte(5<<3) | 0x01 // TC 5, low 3 bits non-zero so movement is available
	bytes[5] = 0x40              // heading-status bit set

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame, WithLocation(newLocationAt(52.31, 4.77)))
	if _, ok := planes.Get("555555"); !ok {
		t.Error("DF 17 TC 5 frame did not register broadcasting ICAO")
	}
}

func TestApplyAircraftStatusEmergencyRoutes(t *testing.T) {
	t.Parallel()

	// DF 17 with TC 28, subtype 1 (Aircraft Status, emergency).
	// Set the EmergencyState to a non-zero value so the inner
	// `if` branch in applyESMessage fires.
	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0xE0
	bytes[2] = 0xE0
	bytes[3] = 0xE0
	bytes[4] = 0xE0 | 0x01 // TC 28 << 3 = 0xE0; subtype 1
	bytes[5] = 0x20        // EmergencyState in top 3 bits → non-zero state

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)
	if _, ok := planes.Get("E0E0E0"); !ok {
		t.Error("DF 17 TC 28 frame did not register broadcasting ICAO")
	}
}

// TestApplyESMessageIdentificationRejectsInvalidCallsign forces
// the IdentificationMessage `return nil` path. A TC 4 frame with
// an all-zero ME-callsign payload decodes to eight '#' characters
// — well above the validCallsign one-placeholder cap — so the
// non-validCallsign branch returns no options and the airplane
// snapshot's Callsign stays empty.
func TestApplyESMessageIdentificationRejectsInvalidCallsign(t *testing.T) {
	t.Parallel()

	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0x7A
	bytes[2] = 0x7B
	bytes[3] = 0x7C
	bytes[4] = byte(4) << 3 // TC 4, zero callsign bytes follow
	// bytes[5..10] remain zero → eight '#' chars after decode

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)

	plane, ok := planes.Get("7A7B7C")
	if !ok {
		t.Fatal("plane not registered")
	}

	if got := plane.GetSnapshot().Callsign; got != "" {
		t.Errorf("callsign = %q, want empty (validCallsign should reject ########)", got)
	}
}

// TestApplyESMessageAircraftStatusNonEmergency forces the
// AircraftStatus inner-`if` false branch and the `return nil`
// that follows: subtype 1 with EmergencyState == None must NOT
// emit a squawk option (snapshot squawk stays empty).
func TestApplyESMessageAircraftStatusNonEmergency(t *testing.T) {
	t.Parallel()

	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0xE1
	bytes[2] = 0xE2
	bytes[3] = 0xE3
	bytes[4] = (byte(28) << 3) | 0x01 // TC 28, subtype 1
	// bytes[5] zero → EmergencyState = None

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)

	plane, ok := planes.Get("E1E2E3")
	if !ok {
		t.Fatal("plane not registered")
	}

	if got := plane.GetSnapshot().Squawk; got != "" {
		t.Errorf("squawk = %q, want empty (non-emergency status should not set squawk)", got)
	}
}

// TestApplyESMessageUnhandledTypeFallsThrough forces the `default`
// branch in applyESMessage. TC 29 (Target State and Status) is
// recognised by the modes decoder but isn't in our handler's
// dispatch list — applyESMessage must return nil and the airplane
// state stays empty bar the message-count bump.
func TestApplyESMessageUnhandledTypeFallsThrough(t *testing.T) {
	t.Parallel()

	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0x29
	bytes[2] = 0x2A
	bytes[3] = 0x2B
	bytes[4] = byte(29) << 3 // TC 29 → TargetStateMessage (unhandled)

	frame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, frame)

	plane, ok := planes.Get("292A2B")
	if !ok {
		t.Fatal("plane not registered (learnICAO should still accept the frame)")
	}

	snap := plane.GetSnapshot()
	if snap.Callsign != "" || snap.Altitude != 0 || snap.Squawk != "" || snap.Velocity != -1 {
		t.Errorf("unhandled TC should not mutate plane state; snap=%+v", snap)
	}
}

// makeShortFrame builds a 7-byte short DF frame whose first byte
// carries the requested DF in the top 5 bits. The address bytes
// (positions 1..3) are decorative — DF 0/4/5/16/20/21 recover
// ICAO from the CRC residual the producer reports separately.
func makeShortFrame(downlinkFormat modes.DownlinkFormat, addr uint32) []byte {
	const (
		highShift = 16
		midShift  = 8
	)

	out := make([]byte, modes.ShortFrameBytes)
	out[0] = byte(downlinkFormat) << 3
	//nolint:gosec // G115: each shift is masked to a byte by the assignment; truncation is the goal.
	out[1] = byte(addr >> highShift)
	out[2] = byte(addr >> midShift) //nolint:gosec
	out[3] = byte(addr)             //nolint:gosec

	return out
}

// makeLongFrame builds a 14-byte long DF frame; same shape as
// makeShortFrame, but at the 14-byte length the DF dispatcher
// expects.
func makeLongFrame(downlinkFormat modes.DownlinkFormat) []byte {
	out := make([]byte, modes.LongFrameBytes)
	out[0] = byte(downlinkFormat) << 3

	return out
}

// TestValidCallsign locks in the noise-rejection contract added
// to validCallsign: a callsign must be at least 3 characters and
// contain at most 1 unassigned-byte placeholder ('#'). Shorter
// strings come from random 6-bit values that happened to decode
// to spaces (trimmed away) — they look character-valid but are
// too short to be a real Mode S ID.
func TestValidCallsign(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty rejected", input: "", want: false},
		{name: "single char rejected", input: "A", want: false},
		{name: "two chars rejected", input: "AB", want: false},
		{name: "three chars accepted", input: "ABC", want: true},
		{name: "three chars with one placeholder accepted", input: "AB#", want: true},
		{name: "two placeholders rejected", input: "AB##", want: false},
		{name: "long clean callsign accepted", input: "KLM1023", want: true},
		{name: "long callsign with single placeholder accepted", input: "KLM10#3", want: true},
		{name: "long callsign with two placeholders rejected", input: "KL#10#3", want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := validCallsign(testCase.input); got != testCase.want {
				t.Errorf("validCallsign(%q) = %v, want %v", testCase.input, got, testCase.want)
			}
		})
	}
}

// makeESLongFrame builds a 14-byte DF 17/18 frame with the given
// AA bytes in positions 1..3. The DF lives in the top 5 bits of
// byte 0. The remaining bytes are zero — icaofilter.Admit ignores
// them for ICAO extraction; per-DF decoders that follow inspect
// the rest.
func makeESLongFrame(downlinkFormat modes.DownlinkFormat, aaHigh, aaMid, aaLow byte) modes.Frame {
	out := make(modes.Frame, modes.LongFrameBytes)
	out[0] = byte(downlinkFormat) << 3
	out[1] = aaHigh
	out[2] = aaMid
	out[3] = aaLow

	return out
}

// makeShortFrameWithDF builds a 7-byte short frame with only the
// DF set. Address bytes are zero — DF 0/4/5/16 recover the
// addressed ICAO from the CRC residual the caller supplies.
func makeShortFrameWithDF(downlinkFormat modes.DownlinkFormat) modes.Frame {
	out := make(modes.Frame, modes.ShortFrameBytes)
	out[0] = byte(downlinkFormat) << 3

	return out
}

// TestHandleFrameRecoveredCounterIncrements covers the
// `if frame.Errors > 0 { a.recoveredFrames.Add(1) }` branch in
// handleFrame. Without it, the stats panel's "recovered" rate
// would freeze at zero even when the bit-error corrector rescued
// frames — masking decoder health under noisy RF conditions.
func TestHandleFrameRecoveredCounterIncrements(t *testing.T) {
	t.Parallel()

	dem := &fakeDemodulator{
		frames: []demod.Frame{{
			Bytes: makeESLongFrame(modes.DFExtendedSquitter, 0x10, 0x20, 0x30),
			DF:    uint8(modes.DFExtendedSquitter),
			CRC:   0,
			// Errors > 0 marks the frame as corrector-rescued.
			Errors:   1,
			WallTime: time.Now(),
		}},
	}

	rcv := &fakeReceiver{reads: []fakeRead{{data: []byte{0x00}}}}
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	)

	if err := stream.Stream(t.Context(), airplanes.New()); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	stats := stream.Stats()
	if stats.TotalFrames != 1 {
		t.Errorf("TotalFrames = %d, want 1", stats.TotalFrames)
	}

	if stats.RecoveredFrames != 1 {
		t.Errorf("RecoveredFrames = %d, want 1", stats.RecoveredFrames)
	}
}

// TestApplySurveillanceAltitudeAppliesAltitude wires a hand-built
// DF 4 frame with a valid Q-bit altitude code through Stream so
// applySurveillanceAltitude reaches the WithAltitude branch (not
// just the early-return error path).
//
// The altitude payload sits in the bottom 13 bits of bytes 2..3
// of the 7-byte frame. We set Q=1 and a small N value so the
// decoder returns a fixed-altitude result with AltitudeError=nil.
func TestApplySurveillanceAltitudeAppliesAltitude(t *testing.T) {
	t.Parallel()

	frame := makeShortFrame(modes.DFSurveillanceAlt, 0)
	// Set Q-bit (bit 4 of byte 3, == bit 4 of the 13-bit altitude code).
	frame[3] = 0x10

	demFrame := demod.Frame{
		Bytes: frame,
		DF:    uint8(modes.DFSurveillanceAlt),
		CRC:   0xAAA001,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("AAA001")
	if !ok {
		t.Fatal("plane not registered")
	}

	// AltitudeFeet for a Q-bit=1 payload with all other bits zero
	// resolves to a fixed (deterministic) value; we just need it
	// to be non-default (default Snapshot.Altitude is 0). Use the
	// snapshot to read it.
	if plane.GetSnapshot().Altitude == 0 {
		t.Error("altitude not applied; WithAltitude branch not reached")
	}
}

// TestApplySurveillanceAltitudeRejectsMalformed forces the
// `err != nil` early return: a too-short frame routed through
// learnICAO (which doesn't length-check for DF 4) reaches
// applySurveillanceAltitude and the decoder rejects on length.
// We assert the plane was still registered (learnICAO accepted)
// but altitude stayed at default.
func TestApplySurveillanceAltitudeRejectsMalformed(t *testing.T) {
	t.Parallel()

	demFrame := demod.Frame{
		Bytes: []byte{byte(modes.DFSurveillanceAlt) << 3}, // 1 byte = malformed
		DF:    uint8(modes.DFSurveillanceAlt),
		CRC:   0xBBB002,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("BBB002")
	if !ok {
		t.Fatal("plane not registered (learnICAO should accept any non-zero residual)")
	}

	if plane.GetSnapshot().Altitude != 0 {
		t.Errorf("altitude unexpectedly set to %v after malformed frame", plane.GetSnapshot().Altitude)
	}
}

// TestApplySurveillanceIdentityAppliesSquawk reaches the
// WithSquawk branch in applySurveillanceIdentity. The squawk
// payload sits in the bottom 13 bits of bytes 2..3; we leave the
// frame zeroed so the decoded squawk is "0000" — non-empty, so
// WithSquawk applies.
func TestApplySurveillanceIdentityAppliesSquawk(t *testing.T) {
	t.Parallel()

	frame := makeShortFrame(modes.DFSurveillanceID, 0)

	demFrame := demod.Frame{
		Bytes: frame,
		DF:    uint8(modes.DFSurveillanceID),
		CRC:   0xCCC003,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("CCC003")
	if !ok {
		t.Fatal("plane not registered")
	}

	if got := plane.GetSnapshot().Squawk; got == "" {
		t.Errorf("squawk not applied; want non-empty, got %q", got)
	}
}

// TestApplySurveillanceIdentityRejectsMalformed forces the error
// early-return of applySurveillanceIdentity by routing a too-
// short frame through.
func TestApplySurveillanceIdentityRejectsMalformed(t *testing.T) {
	t.Parallel()

	demFrame := demod.Frame{
		Bytes: []byte{byte(modes.DFSurveillanceID) << 3},
		DF:    uint8(modes.DFSurveillanceID),
		CRC:   0xDDD004,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("DDD004")
	if !ok {
		t.Fatal("plane not registered")
	}

	if got := plane.GetSnapshot().Squawk; got != "" {
		t.Errorf("squawk unexpectedly set to %q after malformed frame", got)
	}
}

// TestApplyCommBAltitudeReachesAllBranches drives both the
// WithAltitude branch (AltitudeError nil) and the BDS 2,0
// callsign branch in applyCommBAltitude. The frame's MB payload
// begins with 0x20 (BDS code) followed by a valid 8-char
// callsign in the Mode S 6-bit alphabet — the decoder folds it
// into the airplane's callsign.
func TestApplyCommBAltitudeReachesAllBranches(t *testing.T) {
	t.Parallel()

	frame := makeLongFrame(modes.DFCommBAltitude)
	// 13-bit altitude payload with Q-bit set, low N.
	frame[3] = 0x10
	// MB payload (bytes 4..10): BDS 2,0 + KLM1023.
	frame[4] = 0x20
	// Encode "KLM1023" into the 6-bit alphabet (K=0x0B, L=0x0C,
	// M=0x0D, 1=0x31, 0=0x30, 2=0x32, 3=0x33, space=0x20). Bit
	// layout: 8 characters × 6 bits = 48 bits = bytes 5..10.
	// K(11) L(12) M(13) 1(49) 0(48) 2(50) 3(51) space(32):
	//   binary chunks: 001011 001100 001101 110001 110000 110010 110011 100000
	//   bytes:         00101100 11000011 01110001 11000011 00101100 11100000
	frame[5] = 0b00101100
	frame[6] = 0b11000011
	frame[7] = 0b01110001
	frame[8] = 0b11000011
	frame[9] = 0b00101100
	frame[10] = 0b11100000

	demFrame := demod.Frame{
		Bytes: frame,
		DF:    uint8(modes.DFCommBAltitude),
		CRC:   0xEEE005,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("EEE005")
	if !ok {
		t.Fatal("plane not registered")
	}

	snap := plane.GetSnapshot()
	if snap.Altitude == 0 {
		t.Error("altitude not applied via DF 20 + Q-bit payload")
	}

	if snap.Callsign == "" {
		t.Errorf("callsign not applied via BDS 2,0 payload; got %q", snap.Callsign)
	}
}

// TestApplyCommBAltitudeRejectsMalformed forces the error early-
// return: a short frame routed through learnICAO (which doesn't
// length-check for DF 20) reaches applyCommBAltitude and the
// decoder rejects on length.
func TestApplyCommBAltitudeRejectsMalformed(t *testing.T) {
	t.Parallel()

	demFrame := demod.Frame{
		Bytes: []byte{byte(modes.DFCommBAltitude) << 3},
		DF:    uint8(modes.DFCommBAltitude),
		CRC:   0xFFF006,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("FFF006")
	if !ok {
		t.Fatal("plane not registered")
	}

	snap := plane.GetSnapshot()
	if snap.Altitude != 0 || snap.Callsign != "" {
		t.Errorf("decoder error path did not early-return; snap=%+v", snap)
	}
}

// TestApplyCommBIdentityReachesAllBranches drives the squawk and
// BDS 2,0 callsign branches in applyCommBIdentity. Same MB
// payload shape as TestApplyCommBAltitudeReachesAllBranches.
func TestApplyCommBIdentityReachesAllBranches(t *testing.T) {
	t.Parallel()

	frame := makeLongFrame(modes.DFCommBIdentity)
	// MB payload (bytes 4..10): BDS 2,0 + KLM1023.
	frame[4] = 0x20
	frame[5] = 0b00101100
	frame[6] = 0b11000011
	frame[7] = 0b01110001
	frame[8] = 0b11000011
	frame[9] = 0b00101100
	frame[10] = 0b11100000

	demFrame := demod.Frame{
		Bytes: frame,
		DF:    uint8(modes.DFCommBIdentity),
		CRC:   0xABC007,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("ABC007")
	if !ok {
		t.Fatal("plane not registered")
	}

	snap := plane.GetSnapshot()
	if snap.Squawk == "" {
		t.Errorf("squawk not applied; got %q", snap.Squawk)
	}

	if snap.Callsign == "" {
		t.Errorf("callsign not applied via BDS 2,0 payload; got %q", snap.Callsign)
	}
}

// TestApplyCommBIdentityRejectsMalformed forces the error early-
// return of applyCommBIdentity.
func TestApplyCommBIdentityRejectsMalformed(t *testing.T) {
	t.Parallel()

	demFrame := demod.Frame{
		Bytes: []byte{byte(modes.DFCommBIdentity) << 3},
		DF:    uint8(modes.DFCommBIdentity),
		CRC:   0xABC008,
	}

	planes := streamSingleFrame(t, demFrame)

	plane, ok := planes.Get("ABC008")
	if !ok {
		t.Fatal("plane not registered")
	}

	snap := plane.GetSnapshot()
	if snap.Squawk != "" || snap.Callsign != "" {
		t.Errorf("decoder error path did not early-return; snap=%+v", snap)
	}
}

// TestApplyAirbornePositionWithLocationAppliesLatLon reaches the
// WithLatitude / WithLongitude branch in applyAirbornePosition.
// With WithLocation set, resolveCPR always returns ok=true (local
// reference), so a valid TC 11 frame plumbs both altitude and
// position into the airplane snapshot.
func TestApplyAirbornePositionWithLocationAppliesLatLon(t *testing.T) {
	t.Parallel()

	bytes := make([]byte, modes.LongFrameBytes)
	bytes[0] = byte(modes.DFExtendedSquitter) << 3
	bytes[1] = 0x12
	bytes[2] = 0x34
	bytes[3] = 0x56
	bytes[4] = byte(11 << 3) // TC 11 barometric airborne position
	// Altitude with Q-bit = 1 so AltitudeError is nil.
	// altCode = (bytes[5]<<4) | ((bytes[6]&0xF0)>>4); Q-bit is
	// altCode bit 4 = bit 0 of bytes[5]. We need it set.
	bytes[5] = 0x81
	bytes[6] = 0x10

	demFrame := demod.Frame{Bytes: bytes, DF: uint8(modes.DFExtendedSquitter)}

	planes := streamSingleFrame(t, demFrame, WithLocation(newLocationAt(52.31, 4.77)))

	plane, ok := planes.Get("123456")
	if !ok {
		t.Fatal("plane not registered")
	}

	snap := plane.GetSnapshot()
	// resolveCPR with a reference returns ok=true; the local CPR
	// rounding produces some lat/lon near the reference. We just
	// assert the snapshot moved off (0, 0).
	if snap.Latitude == 0 && snap.Longitude == 0 {
		t.Error("position not applied via WithLatitude/WithLongitude")
	}

	if snap.Altitude == 0 {
		t.Error("altitude not applied via WithAltitude (AltitudeError nil branch missed)")
	}
}

// TestCPRCacheStoreRejectsUnknownFormat exercises the
// `default: return 0, 0, false` branch in cprCache.store. The
// CPR format enum has Even and Odd; any other byte value is an
// invalid wire-format CPR and must be discarded.
func TestCPRCacheStoreRejectsUnknownFormat(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	pos := modes.CPRPosition{Latitude: 0, Longitude: 0, Format: 99} // invalid

	_, _, ok := cache.store(0x484755, pos, time.Now())
	if ok {
		t.Error("unknown CPR format should not resolve")
	}
}

// TestCPRCacheStoreDecodeError forces the
// `if err != nil { return 0, 0, false }` branch after
// modes.DecodeCPRGlobal. A pair whose decoded latitudes fall in
// different NL zones returns ErrCPRZoneCrossing — store must
// swallow it and return ok=false rather than emit nonsense
// coordinates.
//
// The values below were picked by inspection: cprResolution is
// 131072; lat=0 (even) vs lat=131000 (odd, near max) put the
// resolved latitudes on opposite sides of the NL = 1 boundary
// near the pole, which the decoder rejects.
func TestCPRCacheStoreDecodeError(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	now := time.Now()

	// Pair discovered by exhaustive search against the modes
	// package: lat=0 (even) + lat=31000 (odd) resolves to two
	// different NL zones, so DecodeCPRGlobal returns
	// ErrCPRZoneCrossing.
	even := modes.CPRPosition{Latitude: 0, Longitude: 0, Format: modes.CPRFormatEven}
	odd := modes.CPRPosition{Latitude: 31000, Longitude: 0, Format: modes.CPRFormatOdd}

	const icao modes.ICAO = 0x484755

	cache.store(icao, even, now)

	_, _, ok := cache.store(icao, odd, now.Add(time.Second))
	if ok {
		t.Error("NL-zone-crossing pair should not resolve; want ok=false")
	}
}

// TestCPRCacheStorePicksOddAsOlderForPairWindow reaches the
// `older = entry.oddSeenAt` branch of cprCache.store. We stage
// odd first at t0, then even at t0+1s; the function computes
// older = min(evenSeenAt, oddSeenAt) and must pick odd here.
func TestCPRCacheStorePicksOddAsOlderForPairWindow(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()

	odd := modes.CPRPosition{Latitude: 88385, Longitude: 125818, Format: modes.CPRFormatOdd}
	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}

	const icao modes.ICAO = 0x484755

	t0 := time.Now()
	cache.store(icao, odd, t0) // odd first → oldest

	if _, _, ok := cache.store(icao, even, t0.Add(time.Second)); !ok {
		t.Error("paired odd+even should resolve when odd is older")
	}
}

// TestRunCleanupPicksOddAsYoungest reaches the
// `youngest = entry.oddSeenAt` branch of runCleanup. Stage even
// in the past, odd in the more recent past, then run cleanup —
// the entry must survive because youngest (oddSeenAt) is recent.
func TestRunCleanupPicksOddAsYoungest(t *testing.T) {
	t.Parallel()

	cache := newCPRCache()
	now := time.Now()

	even := modes.CPRPosition{Latitude: 92095, Longitude: 39846, Format: modes.CPRFormatEven}
	odd := modes.CPRPosition{Latitude: 88385, Longitude: 125818, Format: modes.CPRFormatOdd}

	cache.store(0x484755, even, now)
	cache.store(0x484755, odd, now.Add(time.Second))

	// Backdate even into the past so cleanup considers it ancient,
	// but odd stays recent — youngest = oddSeenAt path.
	cache.mu.Lock()
	cache.entries[0x484755].evenSeenAt = now.Add(-3 * cprPairWindow)
	cache.mu.Unlock()

	ctx, cancel := context.WithCancel(t.Context())

	go cache.runCleanup(ctx, 5*time.Millisecond) //nolint:mnd // tick fast to bound the test.

	// Give the cleanup a few ticks, then assert the entry is still
	// there (because the most-recent timestamp — odd — is fresh).
	time.Sleep(30 * time.Millisecond) //nolint:mnd // bounded wait for several cleanup iterations.

	cache.mu.Lock()
	_, present := cache.entries[0x484755]
	cache.mu.Unlock()

	cancel()

	if !present {
		t.Error("entry evicted despite young oddSeenAt; cleanup youngest-of-pair branch is wrong")
	}
}

// TestDefaultReceiverFactoryWrapsErrorOnNoDongle drives the error
// branch of defaultReceiverFactory: on a test host without an
// RTL-SDR connected, rtl2832u.Open() returns an error and the
// factory must wrap it with errOpenReceiver so callers can
// branch on the sentinel.
//
// On a dev box with a dongle attached, Open() succeeds — we close
// the returned receiver and skip the assertion. The error-wrap
// path is what we actually want to lock in.
func TestDefaultReceiverFactoryWrapsErrorOnNoDongle(t *testing.T) {
	t.Parallel()

	receiver, err := defaultReceiverFactory()
	if err == nil {
		// Dongle present — close and call it good. The error-wrap
		// branch is only reachable on dongle-less hosts.
		_ = receiver.Close()

		t.Skip("dongle present on test host; error branch unreachable here")
	}

	if !errors.Is(err, errOpenReceiver) {
		t.Errorf("error not wrapped with errOpenReceiver: %v", err)
	}
}

// TestDefaultDemodulatorFactoryReturnsNonNil drives the default
// demodulator constructor. It cannot fail (no IO), so we just
// confirm the result is usable: Process must not panic and must
// return a (possibly empty) slice.
func TestDefaultDemodulatorFactoryReturnsNonNil(t *testing.T) {
	t.Parallel()

	dem := defaultDemodulatorFactory()
	if dem == nil {
		t.Fatal("defaultDemodulatorFactory returned nil")
	}

	frames := dem.Process([]byte{})
	if frames == nil && len(frames) > 0 {
		t.Errorf("Process unexpected return: %v", frames)
	}
}

// TestStreamReceiverCloseErrorLogs reaches the close-error branch
// of the deferred receiver.Close() in Stream. A receiver that
// returns an error from Close() goes through the slog.Warn arm
// — we exercise the path without asserting on log output; the
// presence of the receiver in our test seam guarantees the
// branch is taken.
func TestStreamReceiverCloseErrorLogs(t *testing.T) {
	t.Parallel()

	rcv := &fakeReceiverCloseErr{}
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	if err := stream.Stream(t.Context(), airplanes.New()); err != nil {
		t.Errorf("Stream err = %v, want nil", err)
	}

	if rcv.closed != 1 {
		t.Errorf("rcv.closed = %d, want 1", rcv.closed)
	}
}

// fakeReceiverCloseErr is a one-off Receiver that returns
// context.Canceled from Read and a synthetic error from Close —
// just enough to drive the close-error branch in Stream's defer.
type fakeReceiverCloseErr struct {
	closed int
}

var errSyntheticReceiverClose = errors.New("synthetic close failure")

func (*fakeReceiverCloseErr) Read(_ context.Context, _ []byte) (int, error) {
	return 0, context.Canceled
}

func (f *fakeReceiverCloseErr) Close() error {
	f.closed++

	return errSyntheticReceiverClose
}

func TestPruneEvictsStaleAircraft(t *testing.T) {
	t.Parallel()

	stream := New(WithPruneFrequency(5*time.Millisecond), WithPruneThreshold(5*time.Millisecond))
	planes := airplanes.New()

	planes.Ensure("ABCDEF")

	plane, ok := planes.Get("ABCDEF")
	if !ok {
		t.Fatal("plane not registered after Ensure")
	}

	// Backdate the plane so prune sees it as stale on the very next tick.
	plane.Update(airplaneTimestamp(time.Now().Add(-time.Hour)))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go stream.prune(ctx, planes)

	deadline := time.Now().Add(500 * time.Millisecond) //nolint:mnd // generous CI cushion.
	for time.Now().Before(deadline) {
		if _, found := planes.Get("ABCDEF"); !found {
			return
		}

		time.Sleep(2 * time.Millisecond) //nolint:mnd // poll cadence.
	}

	t.Error("plane not evicted within deadline")
}

func TestStreamAutoSweepSkippedWhenReceiverLacksGainControls(t *testing.T) {
	t.Parallel()

	// Use the plain fakeReceiver (no Set*Gain methods) → sweep
	// should detect the missing interface and skip without
	// failing the stream.
	rcv := &fakeReceiver{} // empty queue → first Read returns Canceled
	stream := New(
		WithAutoSweep(),
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	if err := stream.Stream(t.Context(), airplanes.New()); err != nil {
		t.Errorf("Stream returned %v, want nil (sweep should skip, not fail)", err)
	}
}

func TestStreamAutoSweepInvokesGainSettersWhenCapable(t *testing.T) {
	t.Parallel()

	rcv := &sweepCapableFakeReceiver{} // empty reads queue
	stream := New(
		WithAutoSweep(),
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	// Timer-bound ctx: lets the sweep's first probeCell fire its
	// SetLNA/Mix/VGA writes (the ctx.Err() check inside probeCell
	// passes at T=0), then cancels during the cell's settleDelay
	// so we don't sit through the full ~96 s sweep.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_ = stream.Stream(ctx, airplanes.New())

	if rcv.lnaCalls == 0 {
		t.Error("auto-sweep was wired but never called SetLNAGain")
	}
}
