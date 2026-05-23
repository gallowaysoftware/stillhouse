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
	"github.com/gallowaysoftware/stillhouse/qa/internal/runner"
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
	root.AddCommand(planCommand())
	root.AddCommand(runCommand())
	root.AddCommand(reportCommand())
	// TODO: deepen, all — landing after the run + report loop is
	// exercised against a live Stillhouse stack.

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

func planCommand() *cobra.Command {
	var (
		outDir        string
		discoveryFile string
		primerFile    string
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate a breadth-first test plan from discovery.json + primer.md.",
		Long: `plan reads discovery.json (from discover) and primer.md (from
prepare), and runs an LLM phase that emits plan.json — a structured
catalogue of test cases covering happy paths, boundary inputs,
invariant-violation attempts, RLS probes, audit-log gap detection,
B266 reconciliation drift, and race conditions.

Each test case lists which invariant(s) it verifies, the RPC and
UI steps it executes, and the expected outcome. The run phase
consumes plan.json to dispatch ConnectRPC + Playwright probes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := vamp.BuildRoot(func() (*vamp.Pipeline, error) {
				return pipeline.BuildPlan(pipeline.PlanConfig{
					DiscoveryFile: discoveryFile,
					PrimerFile:    primerFile,
				})
			})
			if err != nil {
				return err
			}
			root.SetArgs(append([]string{"run", "--run-dir", outDir}, args...))
			return root.Execute()
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "qa/runs/plan", "Directory plan.json lands in.")
	cmd.Flags().StringVar(&discoveryFile, "discovery", "qa/runs/discover-001/discovery.json", "Path to discovery.json from a prior discover run.")
	cmd.Flags().StringVar(&primerFile, "primer", "qa/runs/prepare-001/primer.md", "Path to primer.md from a prior prepare run.")
	return cmd
}

func runCommand() *cobra.Command {
	var (
		outDir        string
		planFile      string
		backendURL    string
		adminEmail    string
		adminPassword string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute plan.json against a running Stillhouse backend; emit findings.jsonl.",
		Long: `run dispatches every test case in plan.json against the
ConnectRPC endpoint at --backend-url. The caller is responsible for
having the Stillhouse stack already-running and seeded:

  make dev-up backend-dev web-dev seed

The seed cmd prints the admin email + password; pass them to the
runner via --admin-email / --admin-password (or STILLHOUSE_QA_EMAIL
+ STILLHOUSE_QA_PASSWORD env vars). One findings.jsonl row lands per
test case; the report phase aggregates them.

V1 limitations (will be lifted in follow-up work):
  - Each case gets a fresh HTTP session but reuses the seed tenant.
    Fresh-tenant-per-case requires a CreateTenant RPC + RBAC.
  - kind: ui steps are skipped (playwright-go wiring pending).
  - kind: assert types beyond audit_log_has_entry are unimplemented.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if adminEmail == "" {
				adminEmail = os.Getenv("STILLHOUSE_QA_EMAIL")
			}
			if adminPassword == "" {
				adminPassword = os.Getenv("STILLHOUSE_QA_PASSWORD")
			}
			if adminEmail == "" || adminPassword == "" {
				return fmt.Errorf("admin credentials required: pass --admin-email/--admin-password or set STILLHOUSE_QA_EMAIL/STILLHOUSE_QA_PASSWORD")
			}
			return runner.Run(cmd.Context(), runner.Config{
				BackendURL:    backendURL,
				AdminEmail:    adminEmail,
				AdminPassword: adminPassword,
				PlanFile:      planFile,
				OutDir:        outDir,
			})
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "qa/runs/run", "Directory findings.jsonl lands in.")
	cmd.Flags().StringVar(&planFile, "plan", "qa/runs/plan-001/plan.json", "Path to plan.json from a prior plan run.")
	cmd.Flags().StringVar(&backendURL, "backend-url", "http://localhost:8080", "Base URL of the running Stillhouse backend.")
	cmd.Flags().StringVar(&adminEmail, "admin-email", "", "Login email for the seed admin (or STILLHOUSE_QA_EMAIL).")
	cmd.Flags().StringVar(&adminPassword, "admin-password", "", "Login password for the seed admin (or STILLHOUSE_QA_PASSWORD).")
	return cmd
}

func reportCommand() *cobra.Command {
	var (
		findingsFile string
		outFile      string
	)
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Aggregate findings.jsonl into a markdown qa-report.md.",
		Long: `report is a pure-Go aggregator over findings.jsonl. No LLM call —
it groups findings by status / category / priority, surfaces every
failure and error with its evidence, and lists passes by title at
the end. The output lands next to findings.jsonl by default.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runner.Report(runner.ReportConfig{
				FindingsFile: findingsFile,
				OutFile:      outFile,
			})
		},
	}
	cmd.Flags().StringVar(&findingsFile, "findings", "qa/runs/run/findings.jsonl", "Path to findings.jsonl from a prior run.")
	cmd.Flags().StringVar(&outFile, "out", "", "Path to write qa-report.md (defaults to next to findings.jsonl).")
	return cmd
}
