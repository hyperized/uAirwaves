//go:build integration && darwin

package battery

import (
	"context"
	"testing"
)

// TestRunPmset_RealHost covers the production pmset runner's happy
// path against the real command. macOS always ships pmset, so this
// must succeed and yield parseable output.
func TestRunPmset_RealHost(t *testing.T) {
	t.Parallel()

	out, err := runPmset(context.Background())
	if err != nil {
		t.Fatalf("runPmset() error: %v", err)
	}

	if _, perr := parsePmset(out); perr != nil {
		t.Errorf("parsePmset(%q) error: %v", out, perr)
	}
}

// TestRunPmset_CancelledContext covers the runner's error branch:
// a context cancelled before exec makes CommandContext fail, which
// runPmset must wrap.
func TestRunPmset_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runPmset(ctx); err == nil {
		t.Error("expected error from cancelled context")
	}
}
