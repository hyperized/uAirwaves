package location

type fix int
type mode map[fix]string

const (
	unknown fix = iota
	noFix
	twoD
	threeD
)

var fixMode = mode{ //nolint:gochecknoglobals
	unknown: "",
	noFix:   "no fix",
	twoD:    "2D fix",
	threeD:  "3D fix",
}
