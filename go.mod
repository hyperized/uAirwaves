module lab.hyperized.net/hyperized/uAirwaves

go 1.26

require (
	github.com/gdamore/tcell/v2 v2.13.5
	github.com/rivo/tview v0.42.0
	github.com/stratoberry/go-gpsd v1.3.0
)

require (
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/hyperized/demod1090 v0.0.0-00010101000000-000000000000 // indirect
	github.com/hyperized/modes v0.1.0 // indirect
	github.com/hyperized/rtl2832u v0.1.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/term v0.38.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

// Local development: consume demod1090 from the sibling worktree
// until the module is published. Drop this replace once a v0.x.y
// tag exists on the upstream repo.
replace github.com/hyperized/demod1090 => ../demod1090
