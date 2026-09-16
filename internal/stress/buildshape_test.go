package stress

import (
	"strings"
	"testing"
	"time"
)

func TestBuildShape(t *testing.T) {
	const (
		base          = 10.0
		end           = 100.0
		stepSize      = 5.0
		stepDur       = 10 * time.Second
		spikeRPS      = 200.0
		spikeStart    = 5 * time.Second
		spikeDuration = 2 * time.Second
	)

	cases := []struct {
		name       string
		kind       string
		base       float64
		end        float64
		stepSize   float64
		stepDur    time.Duration
		spikeRPS   float64
		spikeStart time.Duration
		spikeDur   time.Duration
		wantNil    bool
		wantErr    string // substring expected in error; "" means no error
		intent     string // short description for error messages
	}{
		{"empty-is-flat", "", base, 0, stepSize, stepDur, 0, 0, 0, true, "", "nil shape (flat)"},
		{"continuous", "continuous", base, end, stepSize, stepDur, 0, 0, 0, false, "", "a continuous ramp"},
		{"continuous-missing-end", "continuous", base, 0, stepSize, stepDur, 0, 0, 0, true, "continuous shape requires --end-rps", "an error"},
		{"stepped", "stepped", base, 0, stepSize, stepDur, 0, 0, 0, false, "", "a stepped ramp"},
		{"spike", "spike", base, 0, stepSize, stepDur, spikeRPS, spikeStart, spikeDuration, false, "", "a spike"},
		{"spike-missing-rps", "spike", base, 0, stepSize, stepDur, 0, spikeStart, spikeDuration, true, "spike shape requires --spike-rps", "an error"},
		{"unknown-kind", "exponential", base, 0, stepSize, stepDur, 0, 0, 0, true, "unknown shape", "an error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shape, err := BuildShape(tc.kind, tc.base, tc.end, tc.stepSize, tc.stepDur, tc.spikeRPS, tc.spikeStart, tc.spikeDur)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("BuildShape(%q) returned nil error, want %s containing %q", tc.kind, tc.intent, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				if shape != nil {
					t.Errorf("expected nil shape alongside the error, got non-nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("BuildShape(%q) failed: %v", tc.kind, err)
			}
			if tc.wantNil {
				if shape != nil {
					t.Errorf("%s: got non-nil shape, want nil", tc.intent)
				}
				return
			}
			if shape == nil {
				t.Fatalf("%s: got nil shape, want a function", tc.intent)
			}
		})
	}
}

func TestBuildShapeReturnedShapesBehave(t *testing.T) {
	// Light functional sanity that the dispatched shapes actually produce the
	// expected rates; full per-shape behavior is covered in shapes_test.go.
	total := 20 * time.Second

	continuous, err := BuildShape("continuous", 10, 100, 5, 10*time.Second, 0, 0, 5*time.Second)
	if err != nil {
		t.Fatalf("continuous: %v", err)
	}
	if got := continuous(total/2, total); !within(got, 55, 0.5) {
		t.Errorf("continuous at midpoint = %v, want ~55", got)
	}

	stepped, err := BuildShape("stepped", 10, 0, 5, 10*time.Second, 0, 0, 5*time.Second)
	if err != nil {
		t.Fatalf("stepped: %v", err)
	}
	if got := stepped(25*time.Second, total); got != 20 {
		t.Errorf("stepped at 25s = %v, want 20", got)
	}

	spike, err := BuildShape("spike", 10, 0, 5, 10*time.Second, 200, 5*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("spike: %v", err)
	}
	if got := spike(6*time.Second, total); got != 200 {
		t.Errorf("spike inside window = %v, want 200", got)
	}
	if got := spike(20*time.Second, total); got != 10 {
		t.Errorf("spike past window = %v, want 10", got)
	}
}
