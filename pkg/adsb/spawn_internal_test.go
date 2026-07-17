package adsb

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

// errSpawnBoom is the panic a task raises so the tests can prove the
// default spawner recovers it instead of aborting the process.
var errSpawnBoom = errors.New("spawned task boom")

// TestNewInstallsDefaultSpawner pins the New default: a stream built
// with no options must carry a non-nil spawn field so Stream can
// launch its helpers.
func TestNewInstallsDefaultSpawner(t *testing.T) {
	t.Parallel()

	if New().spawn == nil {
		t.Error("New().spawn is nil; default spawner not installed")
	}
}

// TestWithWorkerSpawnerStoresInjectedSpawner proves the option
// replaces the spawn field. Invoking the stored spawner must run the
// task through the injected function, not the default.
func TestWithWorkerSpawnerStoresInjectedSpawner(t *testing.T) {
	t.Parallel()

	var (
		spawnerCalled bool
		taskRan       bool
	)

	// The injected spawner runs its task inline, so both flags are
	// set synchronously before the assertions — no atomics needed.
	stream := New(WithWorkerSpawner(func(task func()) {
		spawnerCalled = true

		task()
	}))

	stream.spawn(func() { taskRan = true })

	if !spawnerCalled {
		t.Error("injected spawner was not stored in the spawn field")
	}

	if !taskRan {
		t.Error("stored spawner did not run the task")
	}
}

// TestWithWorkerSpawnerNilKeepsDefault covers the nil guard: passing
// nil must leave the default spawner in place rather than nilling the
// field and panicking at the first Stream call.
func TestWithWorkerSpawnerNilKeepsDefault(t *testing.T) {
	t.Parallel()

	if New(WithWorkerSpawner(nil)).spawn == nil {
		t.Error("spawn is nil after WithWorkerSpawner(nil); default not preserved")
	}
}

// TestStreamSpawnsThreeCtxAwareHelpers drives Stream with a recording
// spawner and proves exactly three helper tasks are launched, and
// that every one of them returns once the Stream context is
// cancelled. Synchronisation is by channel and WaitGroup — no sleeps.
//
// The empty fakeReceiver returns context.Canceled on the first read,
// so Stream returns promptly while the three ctx-loop helpers keep
// running until the test cancels; that lets the test observe the
// spawn count without a blocking receiver.
func TestStreamSpawnsThreeCtxAwareHelpers(t *testing.T) {
	t.Parallel()

	const wantSpawns = 3

	spawned := make(chan struct{}, wantSpawns+1)

	var (
		spawnCount atomic.Int64
		helpers    sync.WaitGroup
	)

	spawn := func(task func()) {
		spawnCount.Add(1)
		helpers.Add(1)

		spawned <- struct{}{}

		go func() {
			defer helpers.Done()

			task()
		}()
	}

	stream := New(
		WithWorkerSpawner(spawn),
		WithReceiverFactory(func() (Receiver, error) { return &fakeReceiver{}, nil }),
		WithDemodulatorFactory(func() Demodulator { return &fakeDemodulator{} }),
		WithPruneFrequency(time.Hour), // keep the prune ticker quiet during the test.
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamErr := make(chan error, 1)

	go func() { streamErr <- stream.Stream(ctx, airplanes.New()) }()

	for range wantSpawns {
		select {
		case <-spawned:
		case <-time.After(2 * time.Second):
			t.Fatal("Stream did not spawn the expected helper task")
		}
	}

	// Stream has finished spawning and returns once the read loop
	// sees context.Canceled; after that no further spawn can occur.
	if err := <-streamErr; err != nil {
		t.Fatalf("Stream returned %v, want nil", err)
	}

	if got := spawnCount.Load(); got != wantSpawns {
		t.Fatalf("spawn called %d times, want %d", got, wantSpawns)
	}

	// Every helper must observe the cancellation and return.
	cancel()

	stopped := make(chan struct{})

	go func() {
		helpers.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("helper tasks did not terminate on context cancel")
	}
}

// TestDefaultSpawnRunsTaskToCompletion covers the happy path: the
// task runs to completion in its own goroutine.
func TestDefaultSpawnRunsTaskToCompletion(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})

	defaultSpawn(func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("defaultSpawn did not run the task")
	}
}

// TestDefaultSpawnRecoversPanic proves the library default spawner
// contains a panicking task: the panic is recovered inside the
// goroutine (a missing recover would abort the whole test binary), so
// the process survives to run the recover-and-log branch.
//
// The deferred close fires while the panic unwinds, which is a
// deterministic signal that the task panicked; the recover-and-log
// then runs on the same goroutine. The log emission itself is not
// asserted: doing so would mean swapping the process-global slog
// default, and this package runs background Stream goroutines that
// log via slog, so a global swap deadlocks under -race.
func TestDefaultSpawnRecoversPanic(t *testing.T) {
	t.Parallel()

	panicked := make(chan struct{})

	defaultSpawn(func() {
		defer close(panicked)

		panic(errSpawnBoom)
	})

	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("defaultSpawn never ran the panicking task")
	}
}
