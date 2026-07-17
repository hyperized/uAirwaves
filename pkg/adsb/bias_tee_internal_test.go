package adsb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

// biasFakeReceiver is fakeReceiver + the biasTeeController surface.
// Used to exercise the bias-tee plumbing without a real dongle.
// Calls are guarded by a mutex so tests reading counters from the
// outside race-free against installBiasTeeController populating the
// slot.
type biasFakeReceiver struct {
	fakeReceiver

	mu         sync.Mutex
	state      bool
	setCalls   int
	getCalls   int
	lastEnable bool
	setErr     error
	getErr     error
}

func (b *biasFakeReceiver) SetBiasTee(enable bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.setCalls++
	b.lastEnable = enable

	if b.setErr != nil {
		return b.setErr
	}

	b.state = enable

	return nil
}

func (b *biasFakeReceiver) GetBiasTee() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.getCalls++

	if b.getErr != nil {
		return false, b.getErr
	}

	return b.state, nil
}

//nolint:nonamedreturns // (setCalls, getCalls) reads clearer named at this signature.
func (b *biasFakeReceiver) snapshot() (setCalls, getCalls int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.setCalls, b.getCalls
}

var (
	errSyntheticBiasSet = errors.New("synthetic bias-tee set failure")
	errSyntheticBiasGet = errors.New("synthetic bias-tee get failure")
)

// TestBiasTeeBeforeStreamUnsupported asserts the public surface
// reports ErrBiasTeeUnsupported and BiasTeeSupported=false before
// any stream has run.
func TestBiasTeeBeforeStreamUnsupported(t *testing.T) {
	t.Parallel()

	stream := New()

	if stream.BiasTeeSupported() {
		t.Error("BiasTeeSupported = true before stream, want false")
	}

	if supported, enabled := stream.BiasTeeState(); supported || enabled {
		t.Errorf("BiasTeeState = (%v, %v) before stream, want (false, false)", supported, enabled)
	}

	if _, err := stream.BiasTeeEnabled(); !errors.Is(err, ErrBiasTeeUnsupported) {
		t.Errorf("BiasTeeEnabled err = %v, want ErrBiasTeeUnsupported", err)
	}

	if err := stream.SetBiasTee(true); !errors.Is(err, ErrBiasTeeUnsupported) {
		t.Errorf("SetBiasTee err = %v, want ErrBiasTeeUnsupported", err)
	}
}

// blockingBiasReceiver is the bias-capable fake driven through the
// full streamSDR lifecycle: Read blocks on the context so the test
// has a deterministic mid-stream window to observe
// BiasTeeSupported = true and call into the bias-tee surface.
type blockingBiasReceiver struct {
	biasFakeReceiver
}

func (*blockingBiasReceiver) Read(ctx context.Context, _ []byte) (int, error) {
	<-ctx.Done()

	//nolint:wrapcheck // test fake; the cancel error is the signal Stream expects.
	return 0, ctx.Err()
}

// nonCapableBiasReceiver is the plain fakeReceiver shape driven
// through streamSDR so the test asserts the type-assertion branch
// in streamSDR leaves biasTee=nil for receivers that don't satisfy
// biasTeeController.
type nonCapableBiasReceiver struct {
	fakeReceiver
}

func (*nonCapableBiasReceiver) Read(ctx context.Context, _ []byte) (int, error) {
	<-ctx.Done()

	//nolint:wrapcheck // test fake; the cancel error is the signal Stream expects.
	return 0, ctx.Err()
}

// TestBiasTeeStreamLifecycle exercises the install + clear edges
// through the real streamSDR path. The fake receiver blocks in
// Read until the test cancels the context; the test observes
// BiasTeeSupported flip true while the stream is alive and false
// once Stream returns. Also exercises the Set/Get pass-through and
// the wrap-error branches via swappable error fakes.
func TestBiasTeeStreamLifecycle(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{}

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	planes := airplanes.New()
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() { done <- stream.Stream(ctx, planes) }()

	if !waitForSupported(t, stream) {
		cancel()
		<-done
		t.Fatal("BiasTeeSupported never flipped true while stream alive")
	}

	exerciseMidStreamBiasTee(t, stream, rcv)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Stream returned: %v", err)
	}

	// Stream exit must leave the chip with bias-tee off. We
	// enabled it mid-stream above; without the shutdown defer
	// disabling it, the fake's state would remain true and an
	// external LNA would stay powered after the app exits.
	if rcv.state {
		t.Error("bias-tee state after Stream exit = true, want false (shutdown defer must disable)")
	}

	if stream.BiasTeeSupported() {
		t.Error("BiasTeeSupported = true after Stream exit, want false")
	}

	if supported, enabled := stream.BiasTeeState(); supported || enabled {
		t.Errorf("BiasTeeState after exit = (%v, %v), want (false, false)", supported, enabled)
	}

	if _, err := stream.BiasTeeEnabled(); !errors.Is(err, ErrBiasTeeUnsupported) {
		t.Errorf("BiasTeeEnabled after exit err = %v, want ErrBiasTeeUnsupported", err)
	}

	if err := stream.SetBiasTee(true); !errors.Is(err, ErrBiasTeeUnsupported) {
		t.Errorf("SetBiasTee after exit err = %v, want ErrBiasTeeUnsupported", err)
	}
}

// exerciseMidStreamBiasTee runs the get/set + counter assertions
// against a live stream. Split out so TestBiasTeeStreamLifecycle's
// body stays within revive's cognitive-complexity gate.
func exerciseMidStreamBiasTee(t *testing.T, stream *ADSB, rcv *blockingBiasReceiver) {
	t.Helper()

	enabled, err := stream.BiasTeeEnabled()
	if err != nil {
		t.Fatalf("BiasTeeEnabled mid-stream: %v", err)
	}

	if enabled {
		t.Error("BiasTeeEnabled = true at start, want false (chip default)")
	}

	if err := stream.SetBiasTee(true); err != nil {
		t.Fatalf("SetBiasTee mid-stream: %v", err)
	}

	// getCalls == 2: the one seed transfer at controller install plus
	// the explicit BiasTeeEnabled poll above. setCalls == 1: the
	// SetBiasTee(true).
	setCalls, getCalls := rcv.snapshot()
	if setCalls != 1 || getCalls != 2 {
		t.Errorf("rcv snapshot = (set %d, get %d), want (1, 2)", setCalls, getCalls)
	}
}

// TestBiasTeeStreamSkipsIncapableReceiver covers the
// non-satisfies branch in streamSDR — a receiver without the
// bias-tee methods leaves the slot nil so the public surface
// keeps reporting unsupported throughout the stream's lifetime.
func TestBiasTeeStreamSkipsIncapableReceiver(t *testing.T) {
	t.Parallel()

	rcv := &nonCapableBiasReceiver{}
	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	planes := airplanes.New()
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() { done <- stream.Stream(ctx, planes) }()

	// Give Stream a moment to enter streamSDR (the slot would have
	// been populated by now if the receiver implemented the
	// interface). The poll deadline is short on purpose — we
	// expect supported to stay false the entire time.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if stream.BiasTeeSupported() {
			cancel()
			<-done
			t.Fatal("BiasTeeSupported flipped true for incapable receiver, want false")
		}

		time.Sleep(time.Millisecond)
	}

	cancel()
	<-done
}

// TestBiasTeeStreamWrapsSetError exercises the SetBiasTee
// error-wrap branch by driving the public surface against a
// running stream backed by a fake whose SetBiasTee returns an
// error.
func TestBiasTeeStreamWrapsSetError(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{biasFakeReceiver: biasFakeReceiver{setErr: errSyntheticBiasSet}}
	stream := newRunningStream(t, rcv)

	if err := stream.SetBiasTee(true); !errors.Is(err, errSyntheticBiasSet) {
		t.Errorf("SetBiasTee err = %v, want wrapping errSyntheticBiasSet", err)
	}
}

// TestBiasTeeStreamWrapsGetError exercises the BiasTeeEnabled
// error-wrap branch against a fake whose GetBiasTee returns an
// error.
func TestBiasTeeStreamWrapsGetError(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{biasFakeReceiver: biasFakeReceiver{getErr: errSyntheticBiasGet}}
	stream := newRunningStream(t, rcv)

	if _, err := stream.BiasTeeEnabled(); !errors.Is(err, errSyntheticBiasGet) {
		t.Errorf("BiasTeeEnabled err = %v, want wrapping errSyntheticBiasGet", err)
	}
}

// newRunningStream starts a Stream against rcv and waits for the
// bias-tee slot to populate. The test's Cleanup cancels the
// context and drains the Stream goroutine so each case stays
// isolated. Returns the stream so the caller can hit
// SetBiasTee / BiasTeeEnabled mid-flight.
func newRunningStream(t *testing.T, rcv *blockingBiasReceiver) *ADSB {
	t.Helper()

	stream := New(
		WithReceiverFactory(func() (Receiver, error) { return rcv, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
	)

	planes := airplanes.New()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- stream.Stream(ctx, planes) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	if !waitForSupported(t, stream) {
		t.Fatal("BiasTeeSupported never flipped true while stream alive")
	}

	return stream
}

// waitForSupported polls BiasTeeSupported up to 1 s. The streamSDR
// goroutine flips the slot synchronously between receiverFactory
// returning and the read loop starting; the bounded wait keeps the
// test deterministic without sleeping a fixed interval.
func waitForSupported(t *testing.T, stream *ADSB) bool {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if stream.BiasTeeSupported() {
			return true
		}

		time.Sleep(time.Millisecond)
	}

	return false
}

// biasStateReadIterations is how many BiasTeeState reads
// TestBiasTeeStateReadsCacheNotController fires to prove the render
// path never reaches the controller. Named so mnd doesn't flag the
// loop bound as a magic number.
const biasStateReadIterations = 50

// TestBiasTeeStateReadsCacheNotController asserts BiasTeeState never
// reaches the controller: after the single seed transfer at install,
// any number of BiasTeeState reads add zero USB calls. This is the
// property that keeps the footer render off the USB control path.
func TestBiasTeeStateReadsCacheNotController(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{}
	stream := newRunningStream(t, rcv)

	// Baseline: install seeds exactly one GetBiasTee, zero sets.
	setBefore, getBefore := rcv.snapshot()
	if setBefore != 0 || getBefore != 1 {
		t.Fatalf("post-install snapshot = (set %d, get %d), want (0, 1) seed", setBefore, getBefore)
	}

	for range biasStateReadIterations {
		if supported, _ := stream.BiasTeeState(); !supported {
			t.Fatal("BiasTeeState supported = false mid-stream, want true")
		}
	}

	setAfter, getAfter := rcv.snapshot()
	if setAfter != setBefore || getAfter != getBefore {
		t.Errorf("controller calls after %d BiasTeeState reads = (set %d, get %d), want unchanged (%d, %d)",
			biasStateReadIterations, setAfter, getAfter, setBefore, getBefore)
	}
}

// TestBiasTeeStateSeededOnInstall asserts the install-time seed
// copies the chip's live bit into the cache: a dongle that boots
// with bias-tee already on reports enabled=true from BiasTeeState
// with no further poll.
func TestBiasTeeStateSeededOnInstall(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{biasFakeReceiver: biasFakeReceiver{state: true}}
	stream := newRunningStream(t, rcv)

	if supported, enabled := stream.BiasTeeState(); !supported || !enabled {
		t.Errorf("BiasTeeState = (%v, %v) after install, want (true, true) seeded from chip", supported, enabled)
	}
}

// TestBiasTeeStateSeedErrorDefaultsOff covers the seed-read failure
// branch in installBiasTeeController: the controller still installs
// (support flips true) but the cached bit defaults to off.
func TestBiasTeeStateSeedErrorDefaultsOff(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{biasFakeReceiver: biasFakeReceiver{getErr: errSyntheticBiasGet}}
	stream := newRunningStream(t, rcv)

	if supported, enabled := stream.BiasTeeState(); !supported || enabled {
		t.Errorf("BiasTeeState = (%v, %v) after failed seed, want (true, false)", supported, enabled)
	}
}

// TestBiasTeeSetUpdatesCache asserts a successful SetBiasTee writes
// through to the cache both directions, so the next BiasTeeState
// reflects the flip without a live poll.
func TestBiasTeeSetUpdatesCache(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{}
	stream := newRunningStream(t, rcv)

	if err := stream.SetBiasTee(true); err != nil {
		t.Fatalf("SetBiasTee(true): %v", err)
	}

	if supported, enabled := stream.BiasTeeState(); !supported || !enabled {
		t.Errorf("BiasTeeState = (%v, %v) after SetBiasTee(true), want (true, true)", supported, enabled)
	}

	if err := stream.SetBiasTee(false); err != nil {
		t.Fatalf("SetBiasTee(false): %v", err)
	}

	if supported, enabled := stream.BiasTeeState(); !supported || enabled {
		t.Errorf("BiasTeeState = (%v, %v) after SetBiasTee(false), want (true, false)", supported, enabled)
	}
}

// TestBiasTeeSetFailureLeavesCache asserts a failed SetBiasTee does
// not disturb the cached bit: the control transfer errored, so the
// render state must keep the last-known-good value.
func TestBiasTeeSetFailureLeavesCache(t *testing.T) {
	t.Parallel()

	rcv := &blockingBiasReceiver{biasFakeReceiver: biasFakeReceiver{setErr: errSyntheticBiasSet}}
	stream := newRunningStream(t, rcv)

	if err := stream.SetBiasTee(true); !errors.Is(err, errSyntheticBiasSet) {
		t.Fatalf("SetBiasTee err = %v, want wrapping errSyntheticBiasSet", err)
	}

	if supported, enabled := stream.BiasTeeState(); !supported || enabled {
		t.Errorf("BiasTeeState = (%v, %v) after failed set, want (true, false) unchanged", supported, enabled)
	}
}

// TestSweepingFlagDefaults asserts a freshly-constructed stream
// reports Sweeping=false. The flag is meaningful only while
// runAutoSweep is active, so its zero-value default has to be
// false or the radar would draw the loading spinner on a fresh
// boot before any sweep was even requested.
func TestSweepingFlagDefaults(t *testing.T) {
	t.Parallel()

	if stream := New(); stream.Sweeping() {
		t.Error("New().Sweeping() = true, want false (no sweep in progress)")
	}
}

// TestSweepingFlagReflectsStore covers the read path of the
// atomic flag. runAutoSweep flips the store-side of this; here we
// poke the atomic directly so the test stays independent of the
// sweep package's runtime behaviour.
func TestSweepingFlagReflectsStore(t *testing.T) {
	t.Parallel()

	stream := New()
	stream.sweeping.Store(true)

	if !stream.Sweeping() {
		t.Error("Sweeping() = false after Store(true), want true")
	}

	stream.sweeping.Store(false)

	if stream.Sweeping() {
		t.Error("Sweeping() = true after Store(false), want false")
	}
}
