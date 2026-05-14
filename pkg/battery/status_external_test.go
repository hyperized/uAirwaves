package battery_test

import (
	"sync"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/battery"
)

func TestNewStatus(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus(battery.WithPercentage(50), battery.WithCharging(true))
	if status.GetPercentage() != 50 {
		t.Errorf("expected percentage 50, got %d", status.GetPercentage())
	}

	if !status.IsCharging() {
		t.Error("expected charging true, got false")
	}

	status2 := battery.NewStatus()
	if status2.GetPercentage() != 0 {
		t.Errorf("expected default percentage 0, got %d", status2.GetPercentage())
	}

	if status2.IsCharging() {
		t.Error("expected default charging false, got true")
	}
}

func TestStatus_Update(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()
	status.Update(battery.WithPercentage(80), battery.WithCharging(false))

	if status.GetPercentage() != 80 {
		t.Errorf("expected percentage 80, got %d", status.GetPercentage())
	}

	if status.IsCharging() {
		t.Error("expected charging false, got true")
	}
}

func TestStatus_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		percentage int8
		charging   bool
		want       string
	}{
		{"charging", 99, true, "~C 99%"},
		{"not charging", 50, false, "50%"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			status := battery.NewStatus(
				battery.WithPercentage(testCase.percentage),
				battery.WithCharging(testCase.charging),
			)
			if got := status.String(); got != testCase.want {
				t.Errorf("String() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestStatus_Concurrency(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()

	var waitGroup sync.WaitGroup
	for index := range 100 {
		waitGroup.Add(2)

		go func(percentage int) {
			defer waitGroup.Done()

			//nolint:gosec
			status.Update(battery.WithPercentage(int8(percentage)))
		}(index)
		go func() {
			defer waitGroup.Done()

			_ = status.String()
		}()
	}

	waitGroup.Wait()
}
