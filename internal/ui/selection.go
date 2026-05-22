package ui

import "sync"

// Selection holds which airplane the operator has picked from the
// right-column list and whether the flight-details panel is
// currently displayed. The plane-list rebuild on every UI tick
// reshuffles indices, so the list's own current-item index is not
// a reliable handle — Selection stores the ICAO instead, and the
// list-rebuild path supplies the index→ICAO mapping so a key
// press can resolve "currently highlighted row" back to a plane.
//
// Thread-safe: the UI updater (1 Hz) writes icaos; the key
// dispatcher reads index→ICAO under the same lock.
type Selection struct {
	mu      sync.RWMutex
	icaos   []string
	selICAO string
	open    bool
}

// NewSelection returns an empty Selection (no plane, details hidden).
func NewSelection() *Selection {
	return &Selection{}
}

// SetICAOs replaces the index→ICAO mapping the UI updater feeds
// into Selection each tick. The slice is stored by reference; the
// caller must not mutate after handing it over.
func (s *Selection) SetICAOs(icaos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.icaos = icaos
}

// OpenAt opens the details panel for the plane at the given list
// index. Out-of-range indices are a no-op — the caller might race
// the list rebuild on a fast key press, and silently dropping the
// open beats panicking.
func (s *Selection) OpenAt(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index < 0 || index >= len(s.icaos) {
		return
	}

	s.selICAO = s.icaos[index]
	s.open = true
}

// Close hides the details panel. The remembered ICAO is left in
// place so a second Enter on the same row reopens the same plane;
// callers that want a hard reset can call Clear.
func (s *Selection) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.open = false
}

// IsOpen reports whether the details panel should be rendered.
func (s *Selection) IsOpen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.open
}

// ICAO returns the ICAO of the plane the user last opened. Empty
// string means "no plane has been opened in this session yet".
func (s *Selection) ICAO() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.selICAO
}

// ICAOAtIndex returns the ICAO at the given index in the current
// list-rebuild snapshot, or the empty string if the index is out
// of range. UpdatePlaneList consults this BEFORE rebuilding so it
// can re-anchor the tview.List's cursor onto the same plane after
// the per-tick Clear()+AddItem cycle — without this the operator
// loses arrow-key cursor progress every second.
func (s *Selection) ICAOAtIndex(index int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if index < 0 || index >= len(s.icaos) {
		return ""
	}

	return s.icaos[index]
}
