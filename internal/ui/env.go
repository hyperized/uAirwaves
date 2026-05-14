// Package ui hosts the small testable helpers that main.go used
// to define inline: env lookup, panic-recovering worker launch,
// header colour selection, key dispatch, plane-list rendering,
// stats tracking, and stats panel rendering. main.go remains
// wiring-only; everything here has 100% test coverage.
package ui

import "os"

// EnvOr returns the value of the named environment variable, or
// fallback when the variable is unset or empty. Mirrors the
// historical main.envOr helper bit-for-bit so the runtime
// configuration behaviour is preserved across the extraction.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
