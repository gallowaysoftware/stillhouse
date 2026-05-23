package pipeline

import (
	"fmt"
	"time"

	"github.com/gallowaysoftware/vibe/vamp"
)

// PlanConfig drives the breadth-first test plan generation. The CLI
// supplies the two file paths the prompt's readFile calls resolve
// against.
type PlanConfig struct {
	// DiscoveryFile is the path to discovery.json from a prior
	// discover run. Required.
	DiscoveryFile string
	// PrimerFile is the path to primer.md from a prior prepare run.
	// Required.
	PrimerFile string
}

// BuildPlan returns the vamp pipeline that turns discovery.json +
// primer.md into a structured test plan (plan.json). The plan is
// breadth-first across every workflow / entity / invariant; the
// deepen phase later adds depth on findings the run phase surfaces.
//
// One long_form text call — the planner's job is essentially a
// large structured-output rewrite of two inputs, no orchestration.
// max_tokens is set high (32k) because a 40-100-case plan with
// payload templates can easily exceed the default budget.
func BuildPlan(cfg PlanConfig) (*vamp.Pipeline, error) {
	if cfg.DiscoveryFile == "" {
		return nil, fmt.Errorf("PlanConfig.DiscoveryFile is required")
	}
	if cfg.PrimerFile == "" {
		return nil, fmt.Errorf("PlanConfig.PrimerFile is required")
	}
	p := vamp.New("stillhouse-qa-plan").
		Describe("Generate a breadth-first test plan from the discovery map and primer.")

	p.Input("discovery_file", vamp.Required(), vamp.WithDefault(cfg.DiscoveryFile),
		vamp.Describe("Path to discovery.json from a prior discover run."))
	p.Input("primer_file", vamp.Required(), vamp.WithDefault(cfg.PrimerFile),
		vamp.Describe("Path to primer.md from a prior prepare run."))

	p.RequireGPUMemory("~30GB during plan (single long_form call).")
	p.CapabilityModel("long_form", vamp.ModelHint{
		MinParams: "27B", MinContext: 131072,
		SuggestedModel: "qwen3.6-27b-mtp-q6_k",
	})

	p.Text("plan").
		Capability("long_form").
		PromptFS(PromptsFS, "plan.md").
		Output("plan.json").
		OutputFormatJSON().
		Param("temperature", 0.3).
		Param("max_tokens", 32768).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Second,
			MaxBackoff:     60 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	return p.Build()
}
