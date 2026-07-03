//go:build darwin

package battery

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// errFakePmset is a static sentinel for the runner-failure path.
var errFakePmset = errors.New("fake pmset failure")

// staticRunner yields fixed pmset output and error on every call.
func staticRunner(out string, err error) pmsetRunner {
	return func(context.Context) (string, error) { return out, err }
}

func TestParsePmset(t *testing.T) {
	t.Parallel()

	const dev = " -InternalBattery-0 (id=4653155)\t"

	tests := []struct {
		name       string
		out        string
		percentage int8
		charging   bool
		wantErr    error
	}{
		{name: "discharging on battery", out: dev + "83%; discharging;", percentage: 83, charging: false},
		{name: "charging on AC", out: dev + "66%; charging;", percentage: 66, charging: true},
		{name: "finishing charge is charging", out: dev + "99%; finishing charge;", percentage: 99, charging: true},
		{name: "charged and full is not charging", out: dev + "100%; charged;", percentage: 100, charging: false},
		{name: "no battery present is unsupported", out: "Now drawing from 'AC Power'\n", wantErr: ErrUnsupported},
		{name: "overflowing percentage errors", out: dev + "99999999999999999999%;", wantErr: strconv.ErrRange},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := parsePmset(testCase.out)

			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("expected error %v, got %v", testCase.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.percentage != testCase.percentage || got.charging != testCase.charging {
				t.Errorf("parsePmset() = %+v, want {%d %t}", got, testCase.percentage, testCase.charging)
			}
		})
	}
}

func TestClampPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int
		want int8
	}{
		{in: -5, want: 0},
		{in: 0, want: 0},
		{in: 50, want: 50},
		{in: 100, want: 100},
		{in: 130, want: 100},
	}

	for _, testCase := range tests {
		if got := clampPercent(testCase.in); got != testCase.want {
			t.Errorf("clampPercent(%d) = %d, want %d", testCase.in, got, testCase.want)
		}
	}
}

func TestNewReader_Darwin(t *testing.T) {
	t.Parallel()

	t.Run("constructs without shelling out", func(t *testing.T) {
		t.Parallel()

		if newReader("") == nil {
			t.Fatal("newReader returned nil")
		}
	})

	t.Run("propagates runner error", func(t *testing.T) {
		t.Parallel()

		read := newReaderWith(staticRunner("", errFakePmset))
		if _, err := read(context.Background()); !errors.Is(err, errFakePmset) {
			t.Errorf("expected %v, got %v", errFakePmset, err)
		}
	})

	t.Run("parses runner output", func(t *testing.T) {
		t.Parallel()

		read := newReaderWith(staticRunner(" -InternalBattery-0\t77%; discharging;", nil))

		got, err := read(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.percentage != 77 || got.charging {
			t.Errorf("unexpected reading: %+v", got)
		}
	})
}
