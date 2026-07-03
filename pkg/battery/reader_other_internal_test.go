//go:build !linux && !darwin

package battery

import (
	"context"
	"errors"
	"testing"
)

// TestNewReader_Unsupported verifies the fallback reader reports
// ErrUnsupported so Watch stops cleanly on platforms without a
// battery backend.
func TestNewReader_Unsupported(t *testing.T) {
	t.Parallel()

	if _, err := newReader("anything")(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got %v", err)
	}
}
