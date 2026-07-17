package ui

import (
	"log/slog"
	"testing"
)

// TestDismissFrontNoBackingArrayCreep locks in the reason
// DismissFront copies down and truncates instead of re-slicing
// with items[1:]: the backing array's start must not advance on
// repeated dismisses, otherwise the head of the array leaks for
// the lifetime of the queue. Re-slicing would move &items[0]
// forward one element per dismiss; copy-down keeps it fixed.
func TestDismissFrontNoBackingArrayCreep(t *testing.T) {
	t.Parallel()

	notifs := NewNotifications()
	notifs.Push(slog.LevelInfo, "a")
	notifs.Push(slog.LevelInfo, "b")
	notifs.Push(slog.LevelInfo, "c")

	base := &notifs.items[0]

	notifs.DismissFront()
	notifs.DismissFront()

	if len(notifs.items) != 1 {
		t.Fatalf("len after two dismisses = %d, want 1", len(notifs.items))
	}

	if &notifs.items[0] != base {
		t.Error("backing array crept forward: &items[0] moved on DismissFront")
	}

	if notifs.items[0].Message != "c" {
		t.Errorf("surviving item = %q, want c", notifs.items[0].Message)
	}
}
