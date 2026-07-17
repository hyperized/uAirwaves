package ui

import (
	"testing"

	"github.com/rivo/tview"
)

// TestRestorePlaneListCursor covers the cursor re-anchor clamps
// directly. UpdatePlaneList only ever calls restorePlaneListCursor
// with a non-negative GetCurrentItem, so the empty-list, past-the-end
// and negative-index guards need white-box exercise to prove they
// clamp into range instead of parking the cursor off the list.
func TestRestorePlaneListCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		items      int
		prevCursor int
		want       int
	}{
		{name: "empty list is a no-op", items: 0, prevCursor: 5, want: 0},
		{name: "in range keeps position", items: 4, prevCursor: 2, want: 2},
		{name: "past end clamps to last", items: 3, prevCursor: 9, want: 2},
		{name: "negative clamps to first", items: 3, prevCursor: -4, want: 0},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			list := tview.NewList()
			for range testCase.items {
				list.AddItem("main", "secondary", 0, nil)
			}

			restorePlaneListCursor(list, testCase.prevCursor)

			if got := list.GetCurrentItem(); got != testCase.want {
				t.Errorf("GetCurrentItem() = %d, want %d", got, testCase.want)
			}
		})
	}
}
