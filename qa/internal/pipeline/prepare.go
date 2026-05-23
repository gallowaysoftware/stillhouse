// Package pipeline holds the vamp pipelines that compose the
// stillhouse-qa loop: prepare → discover → plan → run → deepen →
// report. Each phase lives in its own file so the binary can run
// them individually (`stillhouse-qa prepare`) or chained (`all`).
package pipeline

import (
	"embed"
	"io/fs"
	"time"

	"github.com/gallowaysoftware/vibe/vamp"
)

// assets is the raw embed of the prompts directory. PromptsFS below
// strips the prompts/ prefix via fs.Sub so PromptFS calls reference
// each template by its bare filename — mirroring fake-crime / iitn.
//
//go:embed prompts/*.md
var assets embed.FS

// PromptsFS exposes the embedded prompt templates to every phase.
// Each phase reads its prompt via vamp's PromptFS so a prompt edit
// requires only a rebuild of the QA binary — not a redeploy of the
// vibe daemon.
var PromptsFS fs.FS = mustSub(assets, "prompts")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// PrepareConfig drives the prepare pipeline. Tweak OutDir to land
// the primer somewhere other than qa/runs/prepare; everything else
// is template-driven inside the prompt itself.
type PrepareConfig struct {
	// OutDir is the run dir the pipeline writes primer.md into.
	// Defaults to qa/runs/prepare when empty (the CLI sets this).
	OutDir string
}

// BuildPrepare returns the vamp pipeline that researches the
// Canadian distillery regulatory domain and emits a primer.md the
// downstream discover / plan phases read as ground truth.
//
// Single-stage today: one long_form text call. The prompt is rich
// enough that splitting it across multiple stages would just add
// scheduling overhead without improving output quality. If the
// primer grows to need parallel sub-topic research (e.g. one stage
// per regulation), this is the place to fan it out.
func BuildPrepare(_ PrepareConfig) (*vamp.Pipeline, error) {
	p := vamp.New("stillhouse-qa-prepare").
		Describe("Research Canadian craft-distillery compliance rules and emit a domain primer for QA.")

	p.RequireGPUMemory("~30GB during prepare (single long_form call).")
	p.RequireDiskSpace("<1MB — primer.md is small markdown.")
	p.Note("Primer drives the invariants the QA agent encodes when generating tests. Regenerate any time the domain or model changes.")
	p.CapabilityModel("long_form", vamp.ModelHint{
		MinParams: "27B", MinContext: 131072,
		SuggestedModel: "qwen3.6-27b-mtp-q6_k",
	})

	p.Text("research_primer").
		Capability("long_form").
		PromptFS(PromptsFS, "prepare.md").
		Output("primer.md").
		Param("temperature", 0.3).
		Param("max_tokens", 16384).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Second,
			MaxBackoff:     60 * time.Second,
			RetryOn:        []string{"transient"},
		})

	return p.Build()
}
