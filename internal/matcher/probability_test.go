package matcher

import (
	"testing"

	"cha0s-sim/internal/config"
)

func TestShouldFire(t *testing.T) {
	tests := []struct {
		name      string
		rule      config.Rule
		randValue float64
		want      bool
	}{
		{
			name:      "error rate zero never fires even with rand 0.0",
			rule:      config.Rule{ErrorRate: 0.0},
			randValue: 0.0,
			want:      false,
		},
		{
			name:      "error rate one always fires even with rand 0.999",
			rule:      config.Rule{ErrorRate: 1.0},
			randValue: 0.999,
			want:      true,
		},
		{
			name:      "rate 0.5, rand 0.3 below threshold fires",
			rule:      config.Rule{ErrorRate: 0.5},
			randValue: 0.3,
			want:      true,
		},
		{
			name:      "rate 0.5, rand 0.7 above threshold does not fire",
			rule:      config.Rule{ErrorRate: 0.5},
			randValue: 0.7,
			want:      false,
		},
		{
			name:      "rate 0.5, rand exactly 0.5 boundary does not fire",
			rule:      config.Rule{ErrorRate: 0.5},
			randValue: 0.5,
			want:      false,
		},
		{
			name:      "negative error rate never fires",
			rule:      config.Rule{ErrorRate: -0.1},
			randValue: 0.0,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			randSource := func() float64 { return tc.randValue }
			if got := ShouldFire(tc.rule, randSource); got != tc.want {
				t.Errorf("ShouldFire(rate=%v, rand=%v) = %v, want %v", tc.rule.ErrorRate, tc.randValue, got, tc.want)
			}
		})
	}
}

func TestShouldFireDefaultAlwaysFiresAtRateOne(t *testing.T) {
	rule := config.Rule{ErrorRate: 1.0}
	for i := 0; i < 20; i++ {
		if !ShouldFireDefault(rule) {
			t.Fatalf("ShouldFireDefault() returned false at iteration %d with error_rate 1.0", i)
		}
	}
}
