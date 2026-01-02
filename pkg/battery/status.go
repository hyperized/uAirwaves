package battery

import (
	"fmt"
	"sync"
)

type Status struct {
	percentage int8
	charging   bool
	mu         sync.RWMutex
}

type StatusOption func(*Status)

func NewStatus(opts ...StatusOption) *Status {
	s := &Status{
		percentage: 0,
		charging:   false,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
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

func (s *Status) Update(opts ...StatusOption) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, opt := range opts {
		opt(s)
	}
}

func WithPercentage(percentage int8) StatusOption {
	return func(s *Status) {
		s.percentage = percentage
	}
}

func WithCharging(charging bool) StatusOption {
	return func(s *Status) {
		s.charging = charging
	}
}
