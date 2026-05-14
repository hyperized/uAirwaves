package ui

import (
	"errors"
	"sync"
)

// LaunchWorker runs the supplied worker function in a fresh
// goroutine, recovering panics and forwarding both worker errors
// and recovered panic values (joined with panicSentinel for
// caller-side errors.Is dispatch) to errChan.
//
// This is the only sanctioned way to start a background worker
// in main.go: the panic-recover is what keeps a single rogue
// goroutine from taking down the whole process, and the joined
// sentinel lets the UI updater log "which subsystem panicked"
// when it consumes errChan.
//
// LaunchWorker uses wg.Go (Go 1.25+), which registers the
// goroutine with the WaitGroup automatically — no explicit Add /
// Done. The caller drives shutdown via wg.Wait() in main.
func LaunchWorker(waitGroup *sync.WaitGroup, errChan chan<- error, panicSentinel error, worker func() error) {
	waitGroup.Go(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok {
					errChan <- errors.Join(err, panicSentinel)
				}
			}
		}()

		if err := worker(); err != nil {
			errChan <- err
		}
	})
}
