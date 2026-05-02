package scope

import "sync"

const (
	defaultMin       = 20
	defaultMax       = 200
	defaultSteps     = 4
	defaultCurrent   = 20
	defaultIncrement = 20 // step size for manual range adjustments in nautical miles
)

// Scope represents the current scope of the map.
type Scope struct {
	min, max, steps, current, increment float64
	mu                                  sync.RWMutex
}

// Option is a function that modifies a Scope.
type Option func(*Scope)

// WithCurrent we only really ever need to update the current.
func WithCurrent(current float64) Option {
	return func(r *Scope) { r.current = min(max(current, r.min), r.max) }
}

// New initializes a new Scope.
func New(opts ...Option) *Scope {
	scope := &Scope{
		min:       defaultMin,
		max:       defaultMax,
		steps:     defaultSteps,
		current:   defaultCurrent,
		increment: defaultIncrement,
	}

	for _, opt := range opts {
		opt(scope)
	}

	return scope
}

// Update applies the provided options to an existing Scope.
func (r *Scope) Update(opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, opt := range opts {
		opt(r)
	}
}

// GetMin returns the minimum value of the range.
func (r *Scope) GetMin() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.min
}

// GetMax returns the maximum value of the range.
func (r *Scope) GetMax() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.max
}

// GetSteps returns the number of steps in the range.
func (r *Scope) GetSteps() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.steps
}

// GetCurrent returns the current value of the range.
func (r *Scope) GetCurrent() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.current
}

// GetIncrement returns the step size used for manual range adjustments.
func (r *Scope) GetIncrement() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.increment
}
