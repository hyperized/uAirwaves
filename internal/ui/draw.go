package ui

import (
	"sync/atomic"

	"github.com/rivo/tview"
)

// DrawQueuer is the subset of *tview.Application that
// TryQueueUpdateDraw depends on: enqueue a mutation onto the event
// loop and redraw once it runs. *tview.Application satisfies it.
type DrawQueuer interface {
	QueueUpdateDraw(f func()) *tview.Application
}

// TryQueueUpdateDraw enqueues draw for the event loop without ever
// blocking the caller. It CAS-guards inFlight so at most one draw
// is outstanding; a tick arriving while the previous draw is still
// pending is dropped and returns false (natural coalescing).
//
// The enqueue runs on a detached goroutine, deliberately off the
// LaunchWorker WaitGroup. QueueUpdateDraw blocks until the event
// loop runs draw, but Stop() breaks that loop without draining
// pending updates, so a strand is possible: kept off the WaitGroup,
// a stranded enqueue parks exactly one goroutine forever while
// wg.Wait() still returns and the process exits — on the WaitGroup
// it would wedge shutdown.
func TryQueueUpdateDraw(queuer DrawQueuer, inFlight *atomic.Bool, draw func()) bool {
	if !inFlight.CompareAndSwap(false, true) {
		return false
	}

	go func() {
		queuer.QueueUpdateDraw(draw)
		inFlight.Store(false)
	}()

	return true
}
