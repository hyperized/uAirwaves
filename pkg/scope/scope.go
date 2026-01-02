package scope

import "sync"

type Scope struct {
	min, max, steps, current float64
	mu                       sync.RWMutex
}

type Option func(*Scope)

func New(opts ...Option) *Scope {
	r := &Scope{
		min:     20,
		max:     200,
		steps:   4,
		current: 20,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *Scope) Update(opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, opt := range opts {
		opt(r)
	}
}

// WithCurrent we only really ever need to update the current
func WithCurrent(current float64) Option {
	return func(r *Scope) {
		r.current = min(max(current, r.min), r.max)
	}
}

func (r *Scope) GetMin() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.min
}

func (r *Scope) GetMax() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.max
}

func (r *Scope) GetSteps() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.steps
}

func (r *Scope) GetCurrent() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}
