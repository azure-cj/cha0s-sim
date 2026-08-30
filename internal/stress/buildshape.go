package stress

import (
	"errors"
	"fmt"
	"time"
)

// BuildShape maps a shape-kind string to an RPSShape, applying configuration
// from flag/frontend values. It is the single shared shape-dispatch used by
// both the CLI (internal/cli/stress.go) and the desktop app (app.go), so the
// two callers can never drift apart in semantics.
//
// kind "" returns (nil, nil): a flat rate at base for the whole duration (the
// Soak case — no separate shape constructor exists).
//
// For "continuous", end must be non-zero (a ramp to 0 RPS is meaningless for
// the first version); for "spike", spikeRPS must be non-zero. Both callers
// treat their zero default as "not provided", so value-based detection works
// for the CLI's Changed() checks and the frontend's omitempty fields alike.
func BuildShape(kind string, base, end, stepSize float64, stepDuration time.Duration, spikeRPS float64, spikeStart, spikeDuration time.Duration) (RPSShape, error) {
	switch kind {
	case "":
		return nil, nil
	case "continuous":
		if end == 0 {
			return nil, errors.New("continuous shape requires --end-rps")
		}
		return ContinuousRamp(base, end), nil
	case "stepped":
		return SteppedRamp(base, stepSize, stepDuration), nil
	case "spike":
		if spikeRPS == 0 {
			return nil, errors.New("spike shape requires --spike-rps")
		}
		return Spike(base, spikeRPS, spikeStart, spikeDuration), nil
	default:
		return nil, fmt.Errorf("unknown shape %q: valid values are \"continuous\", \"stepped\", \"spike\" (or \"\" for flat)", kind)
	}
}