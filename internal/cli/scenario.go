package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/stress"
)

var scenarioCmd = &cobra.Command{
	Use:   "scenario",
	Short: "run a multi-step load scenario defined in a YAML file",
	Long: `Run a multi-step load scenario from a YAML file and print per-step results.

Each virtual user loops through the scenario's steps in order until --duration
elapses. Steps are paced by their configured think_time plus real request
latency (there is no target RPS: load is a function of concurrent users and
their natural pacing). Variables extracted from one step's JSON response can be
referenced in later steps as {{name}}.

The target is the base URL all relative step paths are joined against.`,
	RunE: runScenario,
}

func init() {
	scenarioCmd.Flags().String("file", "", "path to the scenario YAML file")
	scenarioCmd.Flags().String("target", "", "base URL to load-test against, e.g. http://localhost:3000")
	scenarioCmd.Flags().Int("vus", 5, "number of concurrent virtual users")
	scenarioCmd.Flags().Duration("duration", 30*time.Second, "how long to keep looping virtual users")
	scenarioCmd.Flags().Duration("timeout", 5*time.Second, "per-request timeout")
	scenarioCmd.MarkFlagRequired("file")
	scenarioCmd.MarkFlagRequired("target")
	stressCmd.AddCommand(scenarioCmd)
}

func runScenario(cmd *cobra.Command, args []string) error {
	file, _ := cmd.Flags().GetString("file")
	target, _ := cmd.Flags().GetString("target")
	vus, _ := cmd.Flags().GetInt("vus")
	duration, _ := cmd.Flags().GetDuration("duration")
	requestTimeout, _ := cmd.Flags().GetDuration("timeout")

	targetURL, err := config.ValidateTargetURL(target)
	if err != nil {
		return fmt.Errorf("invalid --target: %w", err)
	}

	sc, err := stress.LoadScenario(file)
	if err != nil {
		return err
	}

	cfg := stress.ScenarioConfig{
		BaseURL:        targetURL.String(),
		Scenario:       sc,
		VirtualUsers:   vus,
		Duration:       duration,
		RequestTimeout: requestTimeout,
	}

	client := stress.NewClientPool(1000)
	engine := stress.NewScenarioEngine(cfg, client)

	fmt.Printf("Starting scenario: name=%q target=%s vus=%d duration=%s steps=%d\n",
		sc.Name, targetURL.String(), vus, duration, len(sc.Steps))

	// Blocks for the full duration by design: this is a run-and-exit CLI
	// command, not a background server.
	snapshot := engine.Run(context.Background())

	fmt.Printf("Total iterations:    %d\n", snapshot.TotalIterations)
	fmt.Printf("Extraction failures: %d\n", snapshot.ExtractionFailures)
	fmt.Println("Per-step results:")

	// Report steps in the order the YAML defined them, printing a duplicate
	// step name once (shared metrics make a re-printed name redundant).
	seen := make(map[string]bool)
	for _, step := range sc.Steps {
		if seen[step.Name] {
			continue
		}
		seen[step.Name] = true
		if snap, ok := snapshot.StepSnapshots[step.Name]; ok {
			fmt.Printf("\nStep: %s\n", step.Name)
			printSnapshot(snap)
		}
	}
	return nil
}
