package cli

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/stress"
)

var stressCmd = &cobra.Command{
	Use:   "stress",
	Short: "run a load test against a target URL and print results",
	Long: `Run a load test against a target URL and print the resulting metrics.

The test runs to completion and exits: unlike the proxy command this is not a
long-running server. The target rate is flat by default; --shape selects a
scenario shape that varies the rate over time. A flat run with a long
--duration doubles as a Soak test.`,
	RunE: runStress,
}

func init() {
	stressCmd.Flags().String("target", "", "URL to load-test against, e.g. http://localhost:3000")
	stressCmd.Flags().Float64("rps", 10, "target requests per second (base/start rate when a shape is set)")
	stressCmd.Flags().Duration("duration", 10*time.Second, "how long to run the load test")
	stressCmd.Flags().Int("concurrency", 10, "worker pool size")
	stressCmd.Flags().Duration("timeout", 5*time.Second, "per-request timeout")
	stressCmd.Flags().String("shape", "", `scenario shape: "" (flat), "continuous", "stepped", "spike"`)
	stressCmd.Flags().Float64("end-rps", 0, "target RPS to ramp to (continuous shape only)")
	stressCmd.Flags().Float64("step-size", 5, "RPS increase per step (stepped shape only)")
	stressCmd.Flags().Duration("step-duration", 10*time.Second, "how long each step lasts (stepped shape only)")
	stressCmd.Flags().Float64("spike-rps", 0, "RPS during the spike window (spike shape only)")
	stressCmd.Flags().Duration("spike-start", 0, "when the spike window begins (spike shape only)")
	stressCmd.Flags().Duration("spike-duration", 5*time.Second, "how long the spike window lasts (spike shape only)")
	stressCmd.MarkFlagRequired("target")
	rootCmd.AddCommand(stressCmd)
}

func runStress(cmd *cobra.Command, args []string) error {
	target, _ := cmd.Flags().GetString("target")
	rps, _ := cmd.Flags().GetFloat64("rps")
	duration, _ := cmd.Flags().GetDuration("duration")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	requestTimeout, _ := cmd.Flags().GetDuration("timeout")
	shape, _ := cmd.Flags().GetString("shape")
	endRPS, _ := cmd.Flags().GetFloat64("end-rps")
	stepSize, _ := cmd.Flags().GetFloat64("step-size")
	stepDuration, _ := cmd.Flags().GetDuration("step-duration")
	spikeRPS, _ := cmd.Flags().GetFloat64("spike-rps")
	spikeStart, _ := cmd.Flags().GetDuration("spike-start")
	spikeDuration, _ := cmd.Flags().GetDuration("spike-duration")

	var targetURL *url.URL
	targetURL, err := config.ValidateTargetURL(target)
	if err != nil {
		return fmt.Errorf("invalid --target: %w", err)
	}

	cfg := stress.Config{
		TargetURL:      targetURL.String(),
		TargetRPS:      rps,
		Duration:       duration,
		Concurrency:    concurrency,
		RequestTimeout: requestTimeout,
	}

	shapeLabel := "flat"
	shapeFn, err := stress.BuildShape(shape, rps, endRPS, stepSize, stepDuration, spikeRPS, spikeStart, spikeDuration)
	if err != nil {
		return err
	}
	if shapeFn != nil {
		shapeLabel = shape
	}
	cfg.Shape = shapeFn

	client := stress.NewClientPool(1000)
	engine := stress.NewEngine(cfg, client)

	fmt.Printf("Starting stress test: target=%s rps=%g duration=%s concurrency=%d shape=%s\n",
		targetURL.String(), rps, duration, concurrency, shapeLabel)

	// Blocks for the full duration by design: this is a run-and-exit CLI
	// command, not a background server.
	snapshot := engine.Run(context.Background())

	printSnapshot(snapshot)
	return nil
}

func printSnapshot(s stress.MetricsSnapshot) {
	fmt.Printf("Total requests: %d\n", s.TotalRequests)
	fmt.Printf("Total errors:   %d\n", s.TotalErrors)
	fmt.Printf("Error rate:     %.2f%%\n", s.ErrorRate*100)
	fmt.Printf("RPS:            %.2f\n", s.RPS)
	fmt.Printf("P50 latency:    %d ms\n", s.P50Ms)
	fmt.Printf("P95 latency:    %d ms\n", s.P95Ms)
	fmt.Printf("P99 latency:    %d ms\n", s.P99Ms)
	if len(s.ErrorCategories) > 0 {
		fmt.Println("Error categories:")
		keys := make([]string, 0, len(s.ErrorCategories))
		for k := range s.ErrorCategories {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-20s %d\n", k, s.ErrorCategories[k])
		}
	}
}
