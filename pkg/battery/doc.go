// Package battery reads live battery state on a poll loop and exposes it
// through a thread-safe Status. Each GOOS supplies its own reader
// (/sys/class/power_supply on Linux, pmset on macOS); a platform with no
// battery backend returns ErrUnsupported, which Watch treats as terminal
// rather than retrying forever.
package battery
