package ui

import "sync"

// PlaneFilter holds the toggleable filter state for the sidebar
// plane list. positionedOnly defaults to true so the operator
// only sees aircraft with a resolved position — the position-less
// shadow contacts are noise in most workflows.
type PlaneFilter struct {
	mu             sync.RWMutex
	positionedOnly bool
}

// NewPlaneFilter returns a filter with PositionedOnly enabled.
func NewPlaneFilter() *PlaneFilter {
	return &PlaneFilter{positionedOnly: true}
}

// TogglePositionedOnly flips the positioned-only filter.
func (f *PlaneFilter) TogglePositionedOnly() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.positionedOnly = !f.positionedOnly
}

// PositionedOnly reports whether the filter is currently hiding
// position-less planes from the sidebar list.
func (f *PlaneFilter) PositionedOnly() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.positionedOnly
}
