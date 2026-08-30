package stress

import (
	"math"
	"testing"
	"time"
)

func within(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestContinuousRamp(t *testing.T) {
	shape := ContinuousRamp(10, 100)
	total := 20 * time.Second

	cases := []struct {
		name    string
		elapsed time.Duration
		want    float64
	}{
		{"start", 0, 10},
		{"midpoint", total / 2, 55},
		{"end", total, 100},
		{"past-end-clamped", total * 2, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shape(tc.elapsed, total); !within(got, tc.want, 0.5) {
				t.Errorf("RPS at %v = %v, want ~%v", tc.elapsed, got, tc.want)
			}
		})
	}
}

func TestContinuousRampDegenerateTotal(t *testing.T) {
	// Zero/negative total duration can't be divided; must not panic and returns
	// the end value.
	shape := ContinuousRamp(10, 100)
	if got := shape(5*time.Second, 0); got != 100 {
		t.Errorf("RPS with zero total = %v, want 100", got)
	}
}

func TestSteppedRamp(t *testing.T) {
	shape := SteppedRamp(10, 5, 10*time.Second)

	cases := []struct {
		name    string
		elapsed time.Duration
		want    float64
	}{
		{"zero", 0, 10},
		{"just-before-first-step", 9 * time.Second, 10},
		{"first-step-boundary", 10 * time.Second, 15},
		{"inside-second-step", 15 * time.Second, 15},
		{"two-steps", 25 * time.Second, 20},
		{"five-steps", 55 * time.Second, 35},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shape(tc.elapsed, 60*time.Second); got != tc.want {
				t.Errorf("RPS at %v = %v, want %v (hard jump, no interpolation)", tc.elapsed, got, tc.want)
			}
		})
	}
}

func TestSteppedRampNonPositiveStepDuration(t *testing.T) {
	shape := SteppedRamp(10, 5, 0)
	if got := shape(30*time.Second, 60*time.Second); got != 10 {
		t.Errorf("RPS with zero step duration = %v, want 10 (guard against div-by-zero)", got)
	}
}

func TestSpike(t *testing.T) {
	shape := Spike(10, 200, 5*time.Second, 2*time.Second)

	cases := []struct {
		name    string
		elapsed time.Duration
		want    float64
	}{
		{"baseline", 0, 10},
		{"before-window", 4 * time.Second, 10},
		{"window-open", 5 * time.Second, 200},
		{"still-in-window", 6 * time.Second, 200},
		{"window-just-closed", 7 * time.Second, 10},
		{"well-past", 20 * time.Second, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shape(tc.elapsed, 30*time.Second); got != tc.want {
				t.Errorf("RPS at %v = %v, want %v", tc.elapsed, got, tc.want)
			}
		})
	}
}