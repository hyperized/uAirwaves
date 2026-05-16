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

// Notifications is a thread-safe FIFO of pending notifications.
// The slog handler appends to it; the UI thread reads via Front
// for rendering and DismissFront / DismissAll for keybinds.
//
// The lock guards a small slice copy on every operation — the
// queue is expected to stay short (operator-relevant messages,
// not a frame firehose) so this is cheap.
type Notifications struct {
	mu    sync.RWMutex
	items []Notification
}

// NewNotifications returns an empty queue.
func NewNotifications() *Notifications {
	return &Notifications{}
}

// Push appends a notification to the back of the queue.
func (n *Notifications) Push(level slog.Level, message string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.items = append(n.items, Notification{
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	})
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
// queue is empty.
func (n *Notifications) DismissFront() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if len(n.items) > 0 {
		n.items = n.items[1:]
	}
}

// DismissAll empties the queue.
func (n *Notifications) DismissAll() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.items = n.items[:0]
}

// Len returns the queue size.
func (n *Notifications) Len() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return len(n.items)
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
// message), AND adjusts the outer Grid's row 0 height so the
// bar isn't clipped by an under-sized row. Call this from the
// UI update tick.
//
// Why the Grid SetRows call: the parent Flex lives inside Grid
// row 0, which is fixed at 1 row by default. When the
// notification bar wants its own row, the Flex needs 2 rows
// (header + bar), but the Grid clips it to 1 — invisible bar.
// We dynamically grow row 0 to 2 when a notification is queued
// and shrink back to 1 when the queue empties, so the layout
// doesn't waste a row in the common (quiet) case.
//
// Severity → background:
//   - slog.LevelError → red
//   - slog.LevelWarn  → yellow
//   - slog.LevelInfo  → blue
//   - default         → grey
//
// Text colour stays white throughout for contrast.
//
// A " · N more" suffix is appended when the queue holds more
// than one item so the operator knows another message is
// pending behind the current one.
func RenderNotificationBar(
	grid *tview.Grid, parent *tview.Flex, bar *tview.TextView, notifs *Notifications,
) {
	front, ok := notifs.Front()
	if !ok {
		grid.SetRows(1, 0, 1) //nolint:mnd // matches the original SetRows call in main.go.
		parent.ResizeItem(bar, 0, 0)

		return
	}

	grid.SetRows(2, 0, 1) //nolint:mnd // header (1) + notification (1) = 2 rows for the top section.
	parent.ResizeItem(bar, 1, 0)
	bar.SetBackgroundColor(notificationBackground(front.Level))
	bar.SetText(formatNotification(front, notifs.Len()))
}

// notificationBackground maps a slog level to the tcell colour
// the bar paints behind the text.
func notificationBackground(level slog.Level) tcell.Color {
	switch {
	case level >= slog.LevelError:
		return tcell.ColorRed
	case level >= slog.LevelWarn:
		return tcell.ColorYellow
	case level >= slog.LevelInfo:
		return tcell.ColorBlue
	default:
		return tcell.ColorGrey
	}
}

// formatNotification builds the bar's one-line body. Prefixes
// the severity for parseability, appends a "(N more)" hint when
// queue depth exceeds 1, and a "press x to dismiss" reminder so
// new operators don't have to remember the keybind.
func formatNotification(notification Notification, queueLen int) string {
	tag := notification.Level.String()
	more := ""

	if queueLen > 1 {
		more = fmt.Sprintf("  (%d more — X clears all)", queueLen-1)
	}

	return fmt.Sprintf(" [%s] %s%s  ·  press [::b]x[::-] to dismiss",
		tag, notification.Message, more)
}
