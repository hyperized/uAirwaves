package ui_test

import (
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
)

// TestEnvOrUnsetFallback covers the "no environment variable set"
// path. Using a uniquely-named key keeps the test parallel-safe.
func TestEnvOrUnsetFallback(t *testing.T) {
	t.Parallel()

	const key = "UAIRWAVES_TEST_ENVOR_UNSET"

	if got := ui.EnvOr(key, "fallback"); got != "fallback" {
		t.Errorf("EnvOr(%q) = %q, want %q", key, got, "fallback")
	}
}

// TestEnvOrEmptyReturnsFallback drives the "set but empty"
// branch: an empty value is treated the same as unset. t.Setenv
// forbids t.Parallel(), so this case runs serially.
func TestEnvOrEmptyReturnsFallback(t *testing.T) {
	const key = "UAIRWAVES_TEST_ENVOR_EMPTY"

	t.Setenv(key, "")

	if got := ui.EnvOr(key, "fallback"); got != "fallback" {
		t.Errorf("EnvOr(%q) = %q, want %q (empty must fall back)", key, got, "fallback")
	}
}

// TestEnvOrSetReturnsValue drives the "non-empty value" branch.
// Runs serially for the same Setenv reason.
func TestEnvOrSetReturnsValue(t *testing.T) {
	const (
		key  = "UAIRWAVES_TEST_ENVOR_SET"
		want = "real-value"
	)

	t.Setenv(key, want)

	if got := ui.EnvOr(key, "fallback"); got != want {
		t.Errorf("EnvOr(%q) = %q, want %q", key, got, want)
	}
}
