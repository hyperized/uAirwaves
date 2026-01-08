package gps_test

import (
	"context"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/gps"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
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
