package gps_test

import (
	"context"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/pkg/gps"
	"github.com/hyperized/uAirwaves/pkg/location"
)

func TestGPS_PublicInterface(t *testing.T) {
	t.Parallel()

	gpsInstance := gps.New(
		gps.WithGpsAddress("127.0.0.1:2947"),
		gps.WithServiceName("gpsd.service"),
		gps.WithProtocol("tcp"),
	)

	myLoc := location.New()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	// This will fail to connect if no gpsd is running, which is expected in most test environments.
	// We just want to ensure the public methods are accessible and don't panic.
	_ = gpsInstance.Watch(ctx, myLoc)
}

// TestGPS_WithFixCallbackOption pins the public WithFixCallback
// option to the constructor surface: passing it must yield a usable
// instance whose Watch path is callable. The callback's firing
// behaviour on a real TPV is verified white-box in the internal
// suite, where the gpsd session can be faked.
func TestGPS_WithFixCallbackOption(t *testing.T) {
	t.Parallel()

	gpsInstance := gps.New(gps.WithFixCallback(func(time.Time) {}))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	// No gpsd on the default port under test, so this returns
	// without a fix; we only assert the option-configured instance
	// drives Watch without panicking.
	_ = gpsInstance.Watch(ctx, location.New())
}
