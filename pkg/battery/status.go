package battery

import (
	"fmt"
	"sync"
)

// Status keeps track of the current battery state.
type Status struct {
	percentage int8
	charging   bool
	mu         sync.RWMutex
}

// StatusOption is a function that modifies a Status.
type StatusOption func(*Status)

// NewStatus initializes a new Status.
func NewStatus(opts ...StatusOption) *Status {
	status := &Status{
		percentage: 0,
		charging:   false,
		mu:         sync.RWMutex{},
	}

	for _, opt := range opts {
		opt(status)
	}

	return status
}

func (s *Status) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c := ""
	if s.charging {
		c = "~C "
	}

	return fmt.Sprintf("%s%d%%", c, s.percentage)
}

// Update updates the Status with a list of provided StatusOption.
func (s *Status) Update(opts ...StatusOption) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, opt := range opts {
		opt(s)
	}
}

// WithPercentage sets the percentage of the battery.
func WithPercentage(percentage int8) StatusOption {
	return func(s *Status) {
		s.percentage = percentage
	}
}

// WithCharging sets whether the battery is charging.
func WithCharging(charging bool) StatusOption {
	return func(s *Status) {
		s.charging = charging
	}
}
