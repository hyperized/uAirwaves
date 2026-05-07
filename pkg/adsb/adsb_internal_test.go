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
func streamSingleFrame(t *testing.T, frame demod.Frame, opts ...Option) *airplanes.Airplanes {
	t.Helper()

	rcv := &fakeReceiver{reads: []fakeRead{{data: []byte{0x00}}}}
	dem := &fakeDemodulator{frames: []demod.Frame{frame}}

	all := append([]Option{
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return dem }),
	}, opts...)
	stream := New(all...)

	planes := airplanes.New()
	if err := stream.Stream(t.Context(), planes); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	return planes
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
