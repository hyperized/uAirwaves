//go:build !linux && !darwin

package battery

import "context"

// newReader on platforms without a battery backend always reports
// ErrUnsupported, so Watch logs once and stops instead of spinning
// a pointless ticker. The override is irrelevant here.
func newReader(string) reader {
	return func(context.Context) (reading, error) { return reading{}, ErrUnsupported }
}
