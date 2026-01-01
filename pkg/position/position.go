package position

//import (
//	"fmt"
//	"time"
//)
//
//type Options func(position *Position)
//
//type Position struct {
//	Latitude, Longitude, Altitude, Heading, Velocity float64
//	Age                                              time.Duration
//}
//
//func New(options ...Options) *Position {
//	p := &Position{
//		Heading:   0,
//		Velocity:  0,
//		Latitude:  0,
//		Longitude: 0,
//		Altitude:  0,
//		Age:       0,
//	}
//
//	for _, option := range options {
//		option(p)
//	}
//
//	return p
//}
//
//
//func WithAltitude(altitude float64) Options {
//	return func(p *Position) {
//		p.Altitude = altitude
//	}
//}
//
//func (p *Position) String() string {
//	return fmt.Sprintf("%f:%f ^%f", p.Latitude, p.Longitude, p.Altitude)
//}
