package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gallowaysoftware/vibe/vamp"
)

// DiscoverConfig drives the discover pipeline. The CLI stages
// the three concrete inputs into the run dir before calling
// BuildDiscover so the template's `readFile` calls resolve against
// absolute paths regardless of where vamp is run from.
type DiscoverConfig struct {
	// PrimerFile is the path to the primer.md produced by a prior
	// `stillhouse-qa prepare` run. Required.
	PrimerFile string
	// ReadmeFile is the path to Stillhouse's top-level README. The
	// CLI defaults this to <stillhouse_root>/README.md.
	ReadmeFile string
	// ProtoConcatFile is the path to a single file containing every
	// .proto in proto/ concatenated in deterministic order. The CLI
	// builds this file with the `--- file: x.proto ---` separator
	// (see StageProtoConcat in cmd/stillhouse-qa/main.go).
	ProtoConcatFile string
}

// BuildDiscover returns the vamp pipeline that reads the primer +
// README + proto bundle and emits a structured discovery.json
// describing entities, workflows, and invariants. Downstream phases
// (plan, run, deepen, report) consume this JSON.
//
// Single text stage today — same shape as prepare — because every
// input the LLM needs fits comfortably in a 131k context. If the
// proto surface grows beyond that, this is the place to fan out one
// stage per proto file and merge.
func BuildDiscover(cfg DiscoverConfig) (*vamp.Pipeline, error) {
	if cfg.PrimerFile == "" {
		return nil, fmt.Errorf("DiscoverConfig.PrimerFile is required")
	}
	p := vamp.New("stillhouse-qa-discover").
		Describe("Read primer + README + proto and emit a structured entity/workflow/invariant map.")

	p.Input("primer_file", vamp.Required(), vamp.WithDefault(cfg.PrimerFile),
		vamp.Describe("Path to the primer.md from a prior prepare run."))
	p.Input("readme_file", vamp.Required(), vamp.WithDefault(cfg.ReadmeFile),
		vamp.Describe("Path to the Stillhouse README."))
	p.Input("proto_concat_file", vamp.Required(), vamp.WithDefault(cfg.ProtoConcatFile),
		vamp.Describe("Path to a single file containing every proto/*.proto in deterministic order."))

	p.RequireGPUMemory("~30GB during discover (single long_form call).")
	p.RequireDiskSpace("<1MB — discovery.json is small structured JSON.")
	p.CapabilityModel("long_form", vamp.ModelHint{
		MinParams: "27B", MinContext: 131072,
		SuggestedModel: "qwen3.6-27b-mtp-q6_k",
	})

	p.Text("discover").
		Capability("long_form").
		PromptFS(PromptsFS, "discover.md").
		Output("discovery.json").
		OutputFormatJSON().
		Param("temperature", 0.2).
		Param("max_tokens", 32768).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Second,
			MaxBackoff:     60 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	return p.Build()
}

// StageProtoConcat walks protoRoot, finds every *.proto, and writes
// them concatenated into a single file at dstPath with deterministic
// ordering. Each file is prefixed with `// --- file: <rel> ---` so
// the LLM can attribute messages back to their source. Returns the
// list of files included (sorted) for the caller's logging.
//
// Lives in the pipeline package rather than fixture/ because it's
// purely a build-time helper for the discover phase — no app
// lifecycle, no DB, no browser.
func StageProtoConcat(protoRoot, dstPath string) ([]string, error) {
	var files []string
	err := filepath.Walk(protoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".proto") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", protoRoot, err)
	}
	sort.Strings(files)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, f := range files {
		rel, _ := filepath.Rel(protoRoot, f)
		fmt.Fprintf(&b, "// --- file: %s ---\n", rel)
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		b.Write(raw)
		b.WriteString("\n\n")
	}
	if err := os.WriteFile(dstPath, []byte(b.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", dstPath, err)
	}
	return files, nil
}
