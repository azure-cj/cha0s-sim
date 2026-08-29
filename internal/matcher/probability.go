package matcher

import (
	"math/rand"

	"cha0s-sim/internal/config"
)

type RandFunc func() float64

func ShouldFire(rule config.Rule, randSource RandFunc) bool {
	if rule.ErrorRate <= 0 {
		return false
	}
	if rule.ErrorRate >= 1.0 {
		return true
	}
	return randSource() < rule.ErrorRate
}

func ShouldFireDefault(rule config.Rule) bool {
	return ShouldFire(rule, rand.Float64)
}
