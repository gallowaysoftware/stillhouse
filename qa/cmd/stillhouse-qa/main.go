// Command stillhouse-qa drives an LLM-assisted QA loop against the
// Stillhouse app:
//
//	stillhouse-qa prepare        Research CRA distillery rules + emit primer.md.
//	stillhouse-qa discover       Read code + primer → entity/workflow/invariant map.
//	stillhouse-qa plan           Breadth-first test plan from the discovery map.
//	stillhouse-qa run            Execute the plan via ConnectRPC + Playwright probes.
//	stillhouse-qa deepen         Iterative deepening on findings until budget exhausted.
//	stillhouse-qa report         Aggregate findings into qa-report.md.
//	stillhouse-qa all            Run the full sequence in order.
//
// The QA pipeline lives inside the Stillhouse repo on purpose — it
// reads the same proto files and source the backend / frontend
// compile against, so a schema drift can't make the QA agent stale
// without also breaking the build it tests.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/vibe/vamp"

	"github.com/gallowaysoftware/stillhouse/qa/internal/pipeline"
)

func main() {
	root := &cobra.Command{
		Use:   "stillhouse-qa",
		Short: "LLM-assisted QA loop for the Stillhouse distillery app.",
		Long: `stillhouse-qa orchestrates a multi-phase QA sweep against a running
Stillhouse instance. It uses the same vamp pipeline plumbing as the
fake-crime / iitn projects, but the stages target test discovery,
test design, agentic execution, and report generation rather than
audio production.

Bring your own vibe daemon + a running Stillhouse dev stack (see
stillhouse/Makefile: dev-up, backend-dev, web-dev, seed). The QA
binary points at the local Postgres + backend port + web port and
spins probes against them.

The full pipeline produces a markdown report at
qa/runs/<timestamp>/qa-report.md — findings only, no code changes.`,
		SilenceUsage: true,
	}

	root.AddCommand(prepareCommand())
	root.AddCommand(discoverCommand())
	// TODO: plan, run, deepen, report, all — each lands as its own
	// pipeline.go file under internal/pipeline.

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "stillhouse-qa:", err)
		os.Exit(1)
	}
}

func prepareCommand() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Research CRA distillery compliance rules and emit a domain primer.",
		Long: `prepare runs an LLM phase that researches the Canadian craft
distillery domain (LAA math, B266 mechanics, excise stamp lifecycle,
RLS isolation requirements) and writes a primer.md that downstream
phases consume as ground truth for invariant generation.

The primer is regenerated each run so a vibe model upgrade or a
prompt edit surfaces in subsequent QA cycles without staleness.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := vamp.BuildRoot(func() (*vamp.Pipeline, error) {
				return pipeline.BuildPrepare(pipeline.PrepareConfig{
					OutDir: outDir,
				})
			})
			if err != nil {
				return err
			}
			root.SetArgs(append([]string{"run", "--run-dir", outDir}, args...))
			return root.Execute()
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "qa/runs/prepare", "Directory the primer + intermediate stage outputs land in.")
	return cmd
}

func discoverCommand() *cobra.Command {
	var (
		outDir         string
		primerFile     string
		stillhouseRoot string
	)
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Read primer + README + proto → emit a structured entity/workflow/invariant JSON.",
		Long: `discover stages the Stillhouse proto files + README into the run dir,
then runs an LLM phase that maps the app's QA-relevant structure
into discovery.json. The plan / run / deepen phases consume this
JSON to drive test-case generation and probe execution.

By default the discovery looks for the primer at
qa/runs/prepare-001/primer.md (the prepare phase's canonical output
path); override with --primer if a prior run lives elsewhere.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("mkdir out: %w", err)
			}
			protoDst := filepath.Join(outDir, "proto-bundle.txt")
			files, err := pipeline.StageProtoConcat(
				filepath.Join(stillhouseRoot, "proto"), protoDst,
			)
			if err != nil {
				return fmt.Errorf("stage proto bundle: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "staged %d proto files → %s\n", len(files), protoDst)

			root, err := vamp.BuildRoot(func() (*vamp.Pipeline, error) {
				return pipeline.BuildDiscover(pipeline.DiscoverConfig{
					PrimerFile:      primerFile,
					ReadmeFile:      filepath.Join(stillhouseRoot, "README.md"),
					ProtoConcatFile: protoDst,
				})
			})
			if err != nil {
				return err
			}
			root.SetArgs(append([]string{"run", "--run-dir", outDir}, args...))
			return root.Execute()
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "qa/runs/discover", "Directory discovery.json + the proto bundle land in.")
	cmd.Flags().StringVar(&primerFile, "primer", "qa/runs/prepare-001/primer.md", "Path to the primer.md a prior prepare run produced.")
	cmd.Flags().StringVar(&stillhouseRoot, "stillhouse-root", ".", "Path to the Stillhouse repo root (where proto/ + README.md live).")
	return cmd
}
