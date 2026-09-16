package stress

import (
	"time"
)

// ContinuousRamp linearly interpolates the target RPS from startRPS to endRPS
// across the whole duration. A Soak test is just ContinuousRamp(startRPS ==
// endRPS) — or, more simply, Config.Shape == nil with a long Duration — so no
// dedicated Soak shape exists.
func ContinuousRamp(startRPS, endRPS float64) RPSShape {
	return func(elapsed, total time.Duration) float64 {
		if total <= 0 {
			return endRPS
		}
		progress := float64(elapsed) / float64(total)
		if progress > 1 {
			progress = 1
		}
		return startRPS + (endRPS-startRPS)*progress
	}
}

// SteppedRamp increases RPS in hard discrete jumps. stepSize is the RPS gained
// per step, stepDuration is how long each step lasts, and startRPS is the rate
// for the first step. There is no gradual interpolation between steps.
func SteppedRamp(startRPS, stepSize float64, stepDuration time.Duration) RPSShape {
	return func(elapsed, total time.Duration) float64 {
		if stepDuration <= 0 {
			return startRPS
		}
		stepsElapsed := int64(elapsed / stepDuration)
		return startRPS + stepSize*float64(stepsElapsed)
	}
}

// Spike holds baselineRPS, jumps to spikeRPS during [spikeStart,
// spikeStart+spikeDuration), then returns to baselineRPS for the remainder and
// stays there afterwards.
func Spike(baselineRPS, spikeRPS float64, spikeStart, spikeDuration time.Duration) RPSShape {
	return func(elapsed, total time.Duration) float64 {
		if elapsed >= spikeStart && elapsed < spikeStart+spikeDuration {
			return spikeRPS
		}
		return baselineRPS
	}
}
