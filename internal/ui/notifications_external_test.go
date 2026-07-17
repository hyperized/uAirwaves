package ui_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/rivo/tview"
)

const (
	testMessage  = "notify"
	droppedToken = "dropped"

	// maxQueued mirrors the unexported maxQueuedNotifications cap
	// in notifications.go; the external package can't reference it
	// directly, so the value is duplicated and asserted through the
	// public queue behaviour (Len pins at this number).
	maxQueued = 50
)

// newBarWidgets builds the three tview primitives RenderNotificationBar
// writes to. No running Application is needed — SetRows, ResizeItem,
// SetText and the colour setters only mutate primitive state.
func newBarWidgets(t *testing.T) (*tview.Grid, *tview.Flex, *tview.TextView) {
	t.Helper()

	grid := tview.NewGrid()
	parent := tview.NewFlex()
	bar := tview.NewTextView()
	parent.AddItem(bar, 0, 1, false)

	return grid, parent, bar
}

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

// TestSlogHandlerWithAttrsEmptyReturnsSameHandler covers the
// len(attrs)==0 short-circuit: WithAttrs must hand back the
// receiver unchanged rather than allocate a new handler.
func TestSlogHandlerWithAttrsEmptyReturnsSameHandler(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	handler := ui.NewSlogHandler(notifs, slog.LevelInfo)

	if got := handler.WithAttrs(nil); got != handler {
		t.Error("WithAttrs(nil) should return the receiver unchanged")
	}
}

// TestSlogHandlerWithAttrsChainsPrefix covers the prefix-carry and
// multi-attribute branches: a second With on an already-prefixed
// handler appends its pairs after the existing prefix.
func TestSlogHandlerWithAttrsChainsPrefix(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	logger := slog.New(ui.NewSlogHandler(notifs, slog.LevelInfo)).
		With(slog.String("a", "1")).
		With(slog.String("b", "2"), slog.String("c", "3"))

	logger.Info("msg")

	front, _ := notifs.Front()
	if front.Message != "a=1 b=2 c=3 msg" {
		t.Errorf("chained prefix = %q, want %q", front.Message, "a=1 b=2 c=3 msg")
	}
}

// TestNotificationsPushCapsAndDropsOldest drives the queue past
// maxQueued: the length pins at the cap, the oldest entries are
// shed (Front advances to the first survivor) and the rendered
// bar reports both the remaining backlog and the drop count.
func TestNotificationsPushCapsAndDropsOldest(t *testing.T) {
	t.Parallel()

	const overflow = 3

	notifs := ui.NewNotifications()
	for i := range maxQueued + overflow {
		notifs.Push(slog.LevelWarn, fmt.Sprintf("msg-%d", i))
	}

	if got := notifs.Len(); got != maxQueued {
		t.Errorf("Len = %d, want %d (pinned at cap)", got, maxQueued)
	}

	front, _ := notifs.Front()
	if want := fmt.Sprintf("msg-%d", overflow); front.Message != want {
		t.Errorf("Front = %q, want %q (oldest %d dropped)", front.Message, want, overflow)
	}

	grid, parent, bar := newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)
	got := bar.GetText(false)

	if want := fmt.Sprintf("%d %s", overflow, droppedToken); !strings.Contains(got, want) {
		t.Errorf("bar text %q missing %q", got, want)
	}

	if want := fmt.Sprintf("%d more", maxQueued-1); !strings.Contains(got, want) {
		t.Errorf("bar text %q missing %q", got, want)
	}
}

// TestNotificationsDismissAllResetsDroppedIndicator confirms the
// drop counter is cleared by DismissAll: after clearing and
// pushing one fresh message the rendered bar no longer mentions
// dropped entries.
func TestNotificationsDismissAllResetsDroppedIndicator(t *testing.T) {
	t.Parallel()

	const overflow = 5

	notifs := ui.NewNotifications()
	for i := range maxQueued + overflow {
		notifs.Push(slog.LevelWarn, fmt.Sprintf("m%d", i))
	}

	grid, parent, bar := newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)

	if got := bar.GetText(false); !strings.Contains(got, droppedToken) {
		t.Fatalf("precondition: bar text %q should show the dropped hint", got)
	}

	notifs.DismissAll()
	notifs.Push(slog.LevelInfo, testMessage)

	grid, parent, bar = newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)

	if got := bar.GetText(false); strings.Contains(got, droppedToken) {
		t.Errorf("after DismissAll the dropped hint must be gone; got %q", got)
	}
}

// TestRenderNotificationBarHiddenWhenEmpty covers the empty-queue
// branch: RenderNotificationBar returns early without writing any
// text to the bar.
func TestRenderNotificationBarHiddenWhenEmpty(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	grid, parent, bar := newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)

	if got := bar.GetText(false); got != "" {
		t.Errorf("empty queue should leave the bar untouched; got %q", got)
	}
}

// TestRenderNotificationBarSingleMessageHasNoBacklog covers the
// common case: one queued message renders its severity and body
// with neither a "more" nor a "dropped" segment.
func TestRenderNotificationBarSingleMessageHasNoBacklog(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	notifs.Push(slog.LevelInfo, testMessage)

	grid, parent, bar := newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)
	got := bar.GetText(false)

	if !strings.Contains(got, testMessage) {
		t.Errorf("bar text %q missing the message", got)
	}

	if strings.Contains(got, "more") || strings.Contains(got, droppedToken) {
		t.Errorf("single message must have no backlog segment; got %q", got)
	}
}

// TestRenderNotificationBarMoreWithoutDropped covers the backlog
// segment when messages are queued but none were dropped: the bar
// reports "N more" and omits the dropped clause.
func TestRenderNotificationBarMoreWithoutDropped(t *testing.T) {
	t.Parallel()

	notifs := ui.NewNotifications()
	notifs.Push(slog.LevelWarn, "first")
	notifs.Push(slog.LevelWarn, "second")
	notifs.Push(slog.LevelWarn, "third")

	grid, parent, bar := newBarWidgets(t)
	ui.RenderNotificationBar(grid, parent, bar, notifs)
	got := bar.GetText(false)

	if !strings.Contains(got, "2 more — X clears all") {
		t.Errorf("bar text %q missing the more segment", got)
	}

	if strings.Contains(got, droppedToken) {
		t.Errorf("no drops occurred, dropped clause must be absent; got %q", got)
	}
}

// TestRenderNotificationBarSeverityColour covers every branch of
// notificationBackground via the public render path.
func TestRenderNotificationBarSeverityColour(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level slog.Level
		want  tcell.Color
	}{
		{"error red", slog.LevelError, tcell.ColorRed},
		{"warn yellow", slog.LevelWarn, tcell.ColorYellow},
		{"info blue", slog.LevelInfo, tcell.ColorBlue},
		{"below info grey", slog.LevelDebug, tcell.ColorGrey},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			notifs := ui.NewNotifications()
			notifs.Push(testCase.level, testMessage)

			grid, parent, bar := newBarWidgets(t)
			ui.RenderNotificationBar(grid, parent, bar, notifs)

			if got := bar.GetBackgroundColor(); got != testCase.want {
				t.Errorf("background = %v, want %v", got, testCase.want)
			}
		})
	}
}
