module lab.hyperized.net/hyperized/uAirwaves

go 1.26

require (
	github.com/gdamore/tcell/v2 v2.13.9
	github.com/hyperized/demod1090 v0.1.0
	github.com/hyperized/modes v0.2.0
	github.com/hyperized/rtl2832u v0.2.0
	github.com/rivo/tview v0.42.0
	github.com/stratoberry/go-gpsd v1.3.0
)

require (
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/term v0.43.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

// Local development: consume the entire chain — demod1090 +
// rtl2832u + modes — from sibling worktrees while we iterate on
// live-decode bugs. Drop these replaces (and bump the require
// versions) once the chain stabilises and gets re-released.
replace (
	github.com/hyperized/demod1090 => ../demod1090
	github.com/hyperized/modes => ../modes
	github.com/hyperized/rtl2832u => ../rtl2832u
)
