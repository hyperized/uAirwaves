package ui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Notification is one entry in the user-facing notification
// queue: a captured slog record reformatted for in-app display.
type Notification struct {
	Level     slog.Level
	Message   string
	Timestamp time.Time
}

// maxQueuedNotifications caps the pending queue. The TUI routes
// all slog output here, so an unattended session with a flapping
// upstream (a Warn per reconnect attempt) would otherwise grow
// the slice without bound. At the cap the oldest entry is dropped
// in place and counted for the bar to surface.
const maxQueuedNotifications = 50

// Notifications is a thread-safe FIFO of pending notifications.
// The slog handler appends to it; the UI thread reads via Front
// for rendering and DismissFront / DismissAll for keybinds.
//
// The lock guards a small slice copy on every operation — the
// queue is expected to stay short (operator-relevant messages,
// not a frame firehose) so this is cheap. Push holds it at
// maxQueuedNotifications; dropped counts entries shed at the cap
// since the last DismissAll.
type Notifications struct {
	mu      sync.RWMutex
	items   []Notification
	dropped int
}

// NewNotifications returns an empty queue.
func NewNotifications() *Notifications {
	return &Notifications{}
}

// Push appends a notification to the back of the queue. At
// maxQueuedNotifications it drops the oldest by copying the tail
// down within the same backing array and counts the loss, so a
// flapping upstream can't grow the slice without bound.
func (n *Notifications) Push(level slog.Level, message string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	item := Notification{
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	}

	if len(n.items) >= maxQueuedNotifications {
		copy(n.items, n.items[1:])
		n.items[len(n.items)-1] = item
		n.dropped++

		return
	}

	n.items = append(n.items, item)
}

// Front returns the oldest pending notification plus true, or a
// zero Notification + false when the queue is empty. The
// returned Notification is a value copy — safe to retain.
func (n *Notifications) Front() (Notification, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.items) == 0 {
		return Notification{}, false
	}

	return n.items[0], true
}

// DismissFront removes the oldest notification. No-op when the
// queue is empty. Copies the tail down in place and truncates so
// the backing array stops creeping forward on repeated dismisses.
func (n *Notifications) DismissFront() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if len(n.items) == 0 {
		return
	}

	copy(n.items, n.items[1:])
	n.items = n.items[:len(n.items)-1]
}

// DismissAll empties the queue and clears the dropped counter.
func (n *Notifications) DismissAll() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.items = n.items[:0]
	n.dropped = 0
}

// Len returns the queue size.
func (n *Notifications) Len() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return len(n.items)
}

// droppedCount returns the number of notifications shed at the
// cap since the last DismissAll, for the bar's backlog hint.
func (n *Notifications) droppedCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.dropped
}

// SlogHandler is a slog.Handler that captures records into a
// Notifications queue. Used to keep slog output OUT of the
// terminal while tview owns it (otherwise stderr writes corrupt
// the screen layout). Records below minLevel are dropped — the
// queue only carries operator-relevant messages.
//
// Attribute groups are formatted inline as " key=value" pairs
// appended to the message. No structured destination, no JSON;
// the queue is for human-readable one-liners.
type SlogHandler struct {
	notifs   *Notifications
	minLevel slog.Level
	prefix   string
}

// NewSlogHandler returns a SlogHandler that pushes records at
// or above minLevel into notifs. A typical install:
//
//	notifs := ui.NewNotifications()
//	slog.SetDefault(slog.New(ui.NewSlogHandler(notifs, slog.LevelInfo)))
func NewSlogHandler(notifs *Notifications, minLevel slog.Level) *SlogHandler {
	return &SlogHandler{notifs: notifs, minLevel: minLevel}
}

// Enabled reports whether the handler will process a record at
// the given level.
func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

// Handle pushes the record's message + attribute pairs into the
// queue as a single line.
func (h *SlogHandler) Handle(_ context.Context, record slog.Record) error {
	var builder strings.Builder

	if h.prefix != "" {
		builder.WriteString(h.prefix)
		builder.WriteByte(' ')
	}

	builder.WriteString(record.Message)

	record.Attrs(func(attr slog.Attr) bool {
		builder.WriteByte(' ')
		builder.WriteString(attr.Key)
		builder.WriteByte('=')
		builder.WriteString(attr.Value.String())

		return true
	})

	h.notifs.Push(record.Level, builder.String())

	return nil
}

// WithAttrs returns a new handler that prepends the given
// attributes to every record's message line.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	var builder strings.Builder

	if h.prefix != "" {
		builder.WriteString(h.prefix)
		builder.WriteByte(' ')
	}

	for index, attr := range attrs {
		if index > 0 {
			builder.WriteByte(' ')
		}

		builder.WriteString(attr.Key)
		builder.WriteByte('=')
		builder.WriteString(attr.Value.String())
	}

	return &SlogHandler{notifs: h.notifs, minLevel: h.minLevel, prefix: builder.String()}
}

// WithGroup is a no-op for this handler — the in-app display has
// no use for nested grouping, only the flat key=value list. The
// group name is dropped.
func (h *SlogHandler) WithGroup(_ string) slog.Handler {
	return h
}

// RenderNotificationBar wires the queue's current front to the
// bar's text + background, toggles the bar's visibility on the
// parent Flex (height 0 when empty, height 1 when there's a
// message), AND adjusts the outer Grid's row 2 height so the
// bar isn't clipped by an under-sized row. Call this from the
// UI update tick.
//
// Why the Grid SetRows call: the parent Flex (footer + bar)
// lives inside Grid row 2, which is fixed at 1 row by default.
// When the notification bar wants its own row, the Flex needs
// 2 rows (footer + bar), but the Grid clips it to 1 — invisible
// bar. We dynamically grow row 2 to 2 when a notification is
// queued and shrink back to 1 when the queue empties, so the
// layout doesn't waste a row in the common (quiet) case.
//
// Severity → background:
//   - slog.LevelError → red
//   - slog.LevelWarn  → yellow
//   - slog.LevelInfo  → blue
//   - default         → grey
//
// Text colour stays white throughout for contrast.
//
// A "(N more — X clears all)" segment is appended when other
// messages sit behind the current one; when the queue has hit
// its cap the same segment also reports how many were dropped.
func RenderNotificationBar(
	grid *tview.Grid, parent *tview.Flex, bar *tview.TextView, notifs *Notifications,
) {
	front, ok := notifs.Front()
	if !ok {
		grid.SetRows(1, 0, 1) //nolint:mnd // header (1) + content (flex) + footer (1).
		parent.ResizeItem(bar, 0, 0)

		return
	}

	grid.SetRows(1, 0, 2) //nolint:mnd // footer (1) + notification (1) = 2 rows in the bottom section.
	parent.ResizeItem(bar, 1, 0)

	background, text := NotificationColors(front.Level)
	bar.SetBackgroundColor(background)
	bar.SetTextColor(text)
	bar.SetText(formatNotification(front, notifs.Len(), notifs.droppedCount()))
}

// NotificationColors maps a slog level to the (background, text) pair the bar
// paints. A pair rather than just a background: the previous code varied the
// background per severity while leaving the text fixed white, and white on
// yellow is 1.07:1 — warnings were unreadable.
//
//nolint:nonamedreturns // (background, text) reads clearer named at this signature.
func NotificationColors(level slog.Level) (background, text tcell.Color) {
	theme := ActiveTheme()

	switch {
	case level >= slog.LevelError:
		return theme.NotifyErrorBackground, theme.NotifyErrorText
	case level >= slog.LevelWarn:
		return theme.NotifyWarningBackground, theme.NotifyWarningText
	case level >= slog.LevelInfo:
		return theme.NotifyInfoBackground, theme.NotifyInfoText
	default:
		return theme.NotifyDebugBackground, theme.NotifyDebugText
	}
}

// formatNotification builds the bar's one-line body. Prefixes
// the severity for parseability, appends the backlog hint (see
// moreSegment), and a "press x to dismiss" reminder so new
// operators don't have to remember the keybind.
func formatNotification(notification Notification, queueLen, dropped int) string {
	tag := notification.Level.String()

	return fmt.Sprintf(" [%s] %s%s  ·  press [::b]x[::-] to dismiss",
		tag, notification.Message, moreSegment(queueLen, dropped))
}

// moreSegment renders the parenthesised backlog hint: how many
// messages are queued behind the current one and, once the queue
// has hit its cap, how many were dropped. Returns "" when nothing
// is pending so the common single-message case stays uncluttered.
func moreSegment(queueLen, dropped int) string {
	var parts []string

	if queueLen > 1 {
		parts = append(parts, fmt.Sprintf("%d more", queueLen-1))
	}

	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped", dropped))
	}

	if len(parts) == 0 {
		return ""
	}

	return fmt.Sprintf("  (%s — X clears all)", strings.Join(parts, ", "))
}
