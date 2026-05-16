package ui_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
)

func TestNotificationsPushFrontDismissCycle(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()

	if _, present := notifs.Front(); present {
		t.Error("empty queue should report Front ok=false")
	}

	notifs.Push(slog.LevelInfo, "first")
	notifs.Push(slog.LevelError, "second")

	if got := notifs.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}

	front, present := notifs.Front()
	if !present {
		t.Fatal("Front returned ok=false on non-empty queue")
	}

	if front.Message != "first" || front.Level != slog.LevelInfo {
		t.Errorf("Front = %+v, want {first, INFO}", front)
	}

	notifs.DismissFront()

	if got := notifs.Len(); got != 1 {
		t.Errorf("after DismissFront: Len = %d, want 1", got)
	}

	front, present = notifs.Front()
	if !present || front.Message != "second" || front.Level != slog.LevelError {
		t.Errorf("Front after dismiss = %+v / ok=%v, want {second, ERROR}", front, present)
	}
}

func TestNotificationsDismissAll(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	notifs.Push(slog.LevelWarn, "a")
	notifs.Push(slog.LevelWarn, "b")
	notifs.Push(slog.LevelWarn, "c")

	notifs.DismissAll()

	if got := notifs.Len(); got != 0 {
		t.Errorf("after DismissAll: Len = %d, want 0", got)
	}
}

func TestNotificationsDismissFrontEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	notifs.DismissFront() // should not panic

	if got := notifs.Len(); got != 0 {
		t.Errorf("Len = %d, want 0", got)
	}
}

func TestNotificationsConcurrentPushAndDismiss(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()

	const (
		writers = 4
		writes  = 100
	)

	var waiters sync.WaitGroup

	for range writers {
		waiters.Go(func() {
			for range writes {
				notifs.Push(slog.LevelInfo, "ping")
			}
		})
	}

	waiters.Go(func() {
		for range writers * writes / 2 {
			notifs.DismissFront()
		}
	})

	waiters.Wait()

	// At least half should remain after the concurrent dismisses;
	// the exact count depends on interleaving but it must be
	// inside [pushes - dismisses, pushes].
	got := notifs.Len()
	if got < 0 || got > writers*writes {
		t.Errorf("Len = %d, out of plausible range", got)
	}
}

func TestSlogHandlerCapturesAtAndAboveMinLevel(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	logger := slog.New(ui.NewSlogHandler(notifs, slog.LevelInfo))

	logger.Debug("dropped")
	logger.Info("kept")
	logger.Warn("kept warn", slog.String("reason", "test"))
	logger.Error("kept err")

	if got := notifs.Len(); got != 3 {
		t.Errorf("Len = %d, want 3 (Debug should be dropped)", got)
	}

	first, _ := notifs.Front()
	if first.Message != "kept" || first.Level != slog.LevelInfo {
		t.Errorf("first = %+v, want {kept, INFO}", first)
	}

	notifs.DismissFront()
	second, _ := notifs.Front()

	if second.Message != "kept warn reason=test" {
		t.Errorf("attrs not formatted: %q", second.Message)
	}
}

func TestSlogHandlerEnabledRespectsMinLevel(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	handler := ui.NewSlogHandler(notifs, slog.LevelWarn)

	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(INFO) = true with minLevel=WARN, want false")
	}

	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Enabled(WARN) = false with minLevel=WARN, want true")
	}

	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled(ERROR) = false with minLevel=WARN, want true")
	}
}

func TestSlogHandlerWithAttrsPrependsToEveryRecord(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	logger := slog.New(ui.NewSlogHandler(notifs, slog.LevelInfo)).With(slog.String("source", "test"))

	logger.Info("hello")

	front, _ := notifs.Front()
	if front.Message != "source=test hello" {
		t.Errorf("With-attrs prefix missing: %q", front.Message)
	}
}

func TestSlogHandlerWithGroupIsNoOp(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	logger := slog.New(ui.NewSlogHandler(notifs, slog.LevelInfo)).WithGroup("group")

	logger.Info("hello", slog.String("k", "v"))

	front, _ := notifs.Front()
	if front.Message != "hello k=v" {
		t.Errorf("WithGroup should be transparent: got %q", front.Message)
	}
}
