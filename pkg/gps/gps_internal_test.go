package gps

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stratoberry/go-gpsd"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

type mockSession struct {
	mu      sync.RWMutex
	filters map[string]gpsd.Filter
	done    chan bool
	closed  bool
}

func (m *mockSession) AddFilter(f string, filter gpsd.Filter) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.filters[f] = filter
}

func (m *mockSession) Watch() chan bool {
	return m.done
}

func (m *mockSession) Close() error {
	m.closed = true

	return nil
}

func (m *mockSession) getFilter(f string) (gpsd.Filter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter, found := m.filters[f]

	return filter, found
}

func TestWatch(t *testing.T) {
	t.Parallel()

	testSuccessfulWatch(t)
	testSessionDone(t)
	testConnectError(t)
	testInvalidReportType(t)
}

var errDialFailed = errors.New("dial failed")

func testSuccessfulWatch(t *testing.T) {
	t.Helper()
	t.Run("successful watch", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		myLoc := location.New()
		ctx, cancel := context.WithCancel(t.Context())

		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(ctx, myLoc)
		}()

		// Give it a moment to connect and add filters
		time.Sleep(10 * time.Millisecond)

		if filter, found := session.getFilter("TPV"); found {
			filter(&gpsd.TPVReport{
				Mode: 3,
				Lat:  52.5,
				Lon:  13.4,
				Alt:  100.5,
			})
		} else {
			t.Fatal("TPV filter not added")
		}

		lat, lon := myLoc.GetCoordinates()
		if lat != 52.5 || lon != 13.4 {
			t.Errorf("expected coordinates (52.5, 13.4), got (%f, %f)", lat, lon)
		}

		cancel()

		err := <-errCh
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if !session.closed {
			t.Error("expected session to be closed")
		}
	})
}

func testSessionDone(t *testing.T) {
	t.Helper()
	t.Run("session done", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		errCh := make(chan error, 1)

		go func() {
			errCh <- gpsInstance.Watch(t.Context(), location.New())
		}()

		time.Sleep(10 * time.Millisecond)
		close(session.done)

		err := <-errCh
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func testConnectError(t *testing.T) {
	t.Helper()
	t.Run("connect error", func(t *testing.T) {
		t.Parallel()

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return nil, errDialFailed
			}
		})

		err := gpsInstance.Watch(t.Context(), location.New())
		if !errors.Is(err, errDial) {
			t.Errorf("expected errDial, got %v", err)
		}
	})
}

func testInvalidReportType(t *testing.T) {
	t.Helper()
	t.Run("invalid report type", func(t *testing.T) {
		t.Parallel()

		session := &mockSession{
			filters: make(map[string]gpsd.Filter),
			done:    make(chan bool),
		}

		gpsInstance := New(func(gps *GPS) {
			gps.dial = func(_ string) (Session, error) {
				return session, nil
			}
		})

		myLoc := location.New()

		go func() {
			_ = gpsInstance.Watch(t.Context(), myLoc)
		}()

		time.Sleep(10 * time.Millisecond)

		if filter, found := session.getFilter("TPV"); found {
			filter("not a TPV report") // Should not panic
		}
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	gpsInstance := New()
	if gpsInstance.gpsAddress != "127.0.0.1:2947" {
		t.Errorf("expected default gpsAddress 127.0.0.1:2947, got %s", gpsInstance.gpsAddress)
	}

	if gpsInstance.dial == nil {
		t.Error("expected default dial function to be set")
	}

	// Test default dial function error path (requires no gpsd running on default port)
	_, err := gpsInstance.dial("127.0.0.1:0")
	if err == nil {
		t.Error("expected error dialing invalid address")
	}

	t.Run("default dial success", func(t *testing.T) {
		t.Parallel()

		var lc net.ListenConfig

		listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		defer func() {
			_ = listener.Close()
		}()

		go func() {
			conn, err := listener.Accept()
			if err == nil {
				_ = conn.Close()
			}
		}()

		session, err := gpsInstance.dial(listener.Addr().String())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if session != nil {
			_ = session.Close()
		}
	})
}

func TestOptions(t *testing.T) {
	t.Parallel()

	gpsInstance := New(
		WithGpsAddress("1.2.3.4:5678"),
		WithServiceName("custom.service"),
		WithProtocol("udp"),
	)

	if gpsInstance.gpsAddress != "1.2.3.4:5678" {
		t.Errorf("expected gpsAddress 1.2.3.4:5678, got %s", gpsInstance.gpsAddress)
	}

	if gpsInstance.serviceName != "custom.service" {
		t.Errorf("expected serviceName custom.service, got %s", gpsInstance.serviceName)
	}

	if gpsInstance.protocol != "udp" {
		t.Errorf("expected protocol udp, got %s", gpsInstance.protocol)
	}
}
