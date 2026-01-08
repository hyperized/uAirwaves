package scope_test

import (
	"sync"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/scope"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     []scope.Option
		wantMin  float64
		wantMax  float64
		wantStep float64
		wantCurr float64
	}{
		{
			name:     "default values",
			opts:     nil,
			wantMin:  20,
			wantMax:  200,
			wantStep: 4,
			wantCurr: 20,
		},
		{
			name: "custom current via WithCurrent",
			opts: []scope.Option{
				scope.WithCurrent(50),
			},
			wantMin:  20,
			wantMax:  200,
			wantStep: 4,
			wantCurr: 50,
		},
		{
			name: "custom current clamped to min",
			opts: []scope.Option{
				scope.WithCurrent(10),
			},
			wantMin:  20,
			wantMax:  200,
			wantStep: 4,
			wantCurr: 20,
		},
		{
			name: "custom current clamped to max",
			opts: []scope.Option{
				scope.WithCurrent(300),
			},
			wantMin:  20,
			wantMax:  200,
			wantStep: 4,
			wantCurr: 200,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			scopeInstance := scope.New(testCase.opts...)
			if scopeInstance.GetMin() != testCase.wantMin {
				t.Errorf("GetMin() = %v, want %v", scopeInstance.GetMin(), testCase.wantMin)
			}

			if scopeInstance.GetMax() != testCase.wantMax {
				t.Errorf("GetMax() = %v, want %v", scopeInstance.GetMax(), testCase.wantMax)
			}

			if scopeInstance.GetSteps() != testCase.wantStep {
				t.Errorf("GetSteps() = %v, want %v", scopeInstance.GetSteps(), testCase.wantStep)
			}

			if scopeInstance.GetCurrent() != testCase.wantCurr {
				t.Errorf("GetCurrent() = %v, want %v", scopeInstance.GetCurrent(), testCase.wantCurr)
			}
		})
	}
}

func TestScope_Update(t *testing.T) {
	t.Parallel()

	scopeInstance := scope.New()

	scopeInstance.Update(scope.WithCurrent(100))

	if scopeInstance.GetCurrent() != 100 {
		t.Errorf("after Update(WithCurrent(100)), GetCurrent() = %v, want 100", scopeInstance.GetCurrent())
	}

	scopeInstance.Update(scope.WithCurrent(500)) // should clamp to max

	if scopeInstance.GetCurrent() != 200 {
		t.Errorf("after Update(WithCurrent(500)), GetCurrent() = %v, want 200", scopeInstance.GetCurrent())
	}
}

func TestScope_Concurrency(t *testing.T) {
	t.Parallel()

	scopeInstance := scope.New()

	const iterations = 1000

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)

	go func() {
		defer waitGroup.Done()

		for index := range iterations {
			scopeInstance.Update(scope.WithCurrent(float64(index % 200)))
		}
	}()

	go func() {
		defer waitGroup.Done()

		for range iterations {
			_ = scopeInstance.GetCurrent()
			_ = scopeInstance.GetMin()
			_ = scopeInstance.GetMax()
			_ = scopeInstance.GetSteps()
		}
	}()

	waitGroup.Wait()
}
