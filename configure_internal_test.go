package main

import (
	"testing"

	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/radar"
)

// TestConfigureUIWiresEveryComponent drives configureUI end-to-end and
// asserts every widget/domain slot the render path later dereferences
// is populated. One call exercises the whole configure* helper family
// (clock, statusbar, header, footer, plane list, stats, flight-details
// panel, notification bar, right column) plus the adsb.New wiring, so a
// nil slot here means a later renderUI would panic.
func TestConfigureUIWiresEveryComponent(t *testing.T) {
	t.Parallel()

	uic := configureUI(cliConfig{})
	t.Cleanup(uic.cancel)

	checks := []struct {
		name   string
		nonNil bool
	}{
		{"ctx", uic.ctx != nil},
		{"cancel", uic.cancel != nil},
		{"app", uic.app != nil},
		{"errChan", uic.errChan != nil},
		{"myLocation", uic.myLocation != nil},
		{"waitGroup", uic.waitGroup != nil},
		{"planeList", uic.planeList != nil},
		{"adsbStream", uic.adsbStream != nil},
		{"statsTracker", uic.statsTracker != nil},
		{"batteryStatus", uic.batteryStatus != nil},
		{"clock", uic.clock != nil},
		{"statusBar", uic.statusBar != nil},
		{"headerPanel", uic.headerPanel != nil},
		{"notifications", uic.notifications != nil},
		{"notificationBar", uic.notificationBar != nil},
		{"bottomSection", uic.bottomSection != nil},
		{"planeListPanel", uic.planeListPanel != nil},
		{"statsPanel", uic.statsPanel != nil},
		{"rightColumn", uic.rightColumn != nil},
		{"commands", uic.commands != nil},
		{"gpsStatus", uic.gpsStatus != nil},
		{"sourceStatus", uic.sourceStatus != nil},
		{"footer", uic.footer != nil},
		{"selection", uic.selection != nil},
		{"flightDetailsText", uic.flightDetailsText != nil},
		{"flightDetailsMini", uic.flightDetailsMini != nil},
		{"flightDetailsPanel", uic.flightDetailsPanel != nil},
		{"selfLocator", uic.selfLocator != nil},
		{"gpsLastFix", uic.gpsLastFix != nil},
	}

	for _, check := range checks {
		if !check.nonNil {
			t.Errorf("configureUI left %s nil", check.name)
		}
	}
}

// TestConfigureGridPlacesPagesAndDefaultsToRadar builds the grid from a
// fully-configured uiComponents (radar panel supplied as main does) and
// asserts configureGrid populates the left Pages widget with the radar
// showing first — the default front page before a plane is opened.
func TestConfigureGridPlacesPagesAndDefaultsToRadar(t *testing.T) {
	t.Parallel()

	uic := configureUI(cliConfig{})
	t.Cleanup(uic.cancel)

	uic.radarPanel = radar.New(uic.planeList, uic.myLocation, uic.adsbStream)

	grid := configureGrid(uic)
	if grid == nil {
		t.Fatal("configureGrid returned nil")
	}

	if uic.leftPages == nil {
		t.Fatal("configureGrid did not populate leftPages")
	}

	name, _ := uic.leftPages.GetFrontPage()
	if name != leftPageRadar {
		t.Errorf("front page = %q, want %q", name, leftPageRadar)
	}
}

// TestBuildADSBOptionsAppendsBiasTeeAndSweep pins the two local-SDR
// branches missed by the source-routing table: --bias-t appends the
// bias-tee receiver factory and --auto-sweep appends the sweep option.
// Options are opaque funcs with no read-back, so each branch is
// observed by count against the same SDR baseline built without it.
func TestBuildADSBOptionsAppendsBiasTeeAndSweep(t *testing.T) {
	t.Parallel()

	baseline := len(buildADSBOptions(cliConfig{}, location.New(), nil, nil, nil))

	for _, testCase := range []struct {
		name      string
		cfg       cliConfig
		wantDelta int
	}{
		{"bias-tee adds the factory", cliConfig{biasTee: true}, 1},
		{"auto-sweep adds the sweep", cliConfig{autoSweep: true}, 1},
		{"both add two options", cliConfig{biasTee: true, autoSweep: true}, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := len(buildADSBOptions(testCase.cfg, location.New(), nil, nil, nil))
			if got != baseline+testCase.wantDelta {
				t.Errorf("option count = %d, want %d (baseline %d + %d)",
					got, baseline+testCase.wantDelta, baseline, testCase.wantDelta)
			}
		})
	}
}
