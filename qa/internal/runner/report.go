package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReportConfig drives the report aggregator.
type ReportConfig struct {
	// FindingsFile is the path to findings.jsonl produced by Run.
	FindingsFile string
	// OutFile is where the markdown report is written. Defaults to
	// <findings-dir>/qa-report.md.
	OutFile string
}

// Report reads findings.jsonl and writes a structured markdown
// report. Pure Go aggregation — no LLM call — so the full QA loop
// can finish even when vibe is unavailable.
func Report(cfg ReportConfig) error {
	f, err := os.Open(cfg.FindingsFile)
	if err != nil {
		return fmt.Errorf("open findings: %w", err)
	}
	defer f.Close()

	var findings []Finding
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var fnd Finding
		if err := json.Unmarshal([]byte(line), &fnd); err != nil {
			return fmt.Errorf("parse line: %w", err)
		}
		findings = append(findings, fnd)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	outPath := cfg.OutFile
	if outPath == "" {
		outPath = filepath.Join(filepath.Dir(cfg.FindingsFile), "qa-report.md")
	}
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	defer out.Close()

	writeReport(out, findings)
	fmt.Fprintf(os.Stderr, "report → %s\n", outPath)
	return nil
}

func writeReport(w io.Writer, findings []Finding) {
	fmt.Fprintln(w, "# Stillhouse QA Report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "_Generated %s — %d test cases executed._\n", time.Now().Format(time.RFC3339), len(findings))
	fmt.Fprintln(w)

	// Summary table.
	byStatus := map[string]int{}
	byCategory := map[string]map[string]int{}
	byPriority := map[string]map[string]int{}
	for _, f := range findings {
		byStatus[f.Status]++
		if byCategory[f.Category] == nil {
			byCategory[f.Category] = map[string]int{}
		}
		byCategory[f.Category][f.Status]++
		if byPriority[f.Priority] == nil {
			byPriority[f.Priority] = map[string]int{}
		}
		byPriority[f.Priority][f.Status]++
	}

	fmt.Fprintln(w, "## Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Status | Count |")
	fmt.Fprintln(w, "|--------|------:|")
	for _, k := range sortedKeys(byStatus) {
		fmt.Fprintf(w, "| %s | %d |\n", k, byStatus[k])
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "### By category")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Category | Pass | Fail | Skipped | Error |")
	fmt.Fprintln(w, "|----------|-----:|-----:|--------:|------:|")
	for _, cat := range sortedKeys(byCategory) {
		row := byCategory[cat]
		fmt.Fprintf(w, "| %s | %d | %d | %d | %d |\n",
			cat, row["pass"], row["fail"], row["skipped"], row["error"])
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "### By priority")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Priority | Pass | Fail | Skipped | Error |")
	fmt.Fprintln(w, "|----------|-----:|-----:|--------:|------:|")
	for _, prio := range sortedKeys(byPriority) {
		row := byPriority[prio]
		fmt.Fprintf(w, "| %s | %d | %d | %d | %d |\n",
			prio, row["pass"], row["fail"], row["skipped"], row["error"])
	}
	fmt.Fprintln(w)

	// Failures + errors first (most actionable).
	writeSection(w, "## Failures", findings, func(f Finding) bool { return f.Status == "fail" })
	writeSection(w, "## Errors", findings, func(f Finding) bool { return f.Status == "error" })

	// Passes folded into a single short list (titles only) so a
	// human can scan what worked without scrolling endlessly.
	var passes []Finding
	for _, f := range findings {
		if f.Status == "pass" {
			passes = append(passes, f)
		}
	}
	if len(passes) > 0 {
		fmt.Fprintln(w, "## Passes")
		fmt.Fprintln(w)
		for _, f := range passes {
			fmt.Fprintf(w, "- `%s` — %s (%s, %s)\n", f.CaseID, f.Title, f.Category, f.Priority)
		}
		fmt.Fprintln(w)
	}
}

// writeSection renders one detailed section (failures, errors). Each
// finding gets its title, priority, the invariants it verifies, and
// the first failing step's body sample as evidence.
func writeSection(w io.Writer, header string, findings []Finding, match func(Finding) bool) {
	var matched []Finding
	for _, f := range findings {
		if match(f) {
			matched = append(matched, f)
		}
	}
	if len(matched) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)
	for _, f := range matched {
		fmt.Fprintf(w, "### `%s` — %s\n", f.CaseID, f.Title)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "- **Priority**: %s\n", f.Priority)
		fmt.Fprintf(w, "- **Category**: %s\n", f.Category)
		if len(f.VerifiesInvariants) > 0 {
			fmt.Fprintf(w, "- **Verifies**: %s\n", strings.Join(f.VerifiesInvariants, ", "))
		}
		if len(f.PrimerSections) > 0 {
			fmt.Fprintf(w, "- **Primer**: %s\n", strings.Join(f.PrimerSections, ", "))
		}
		if f.Reason != "" {
			fmt.Fprintf(w, "- **Reason**: %s\n", f.Reason)
		}
		fmt.Fprintln(w)
		for _, sr := range f.StepResults {
			if sr.Pass {
				continue
			}
			fmt.Fprintf(w, "Step %d (%s", sr.Index, sr.Kind)
			if sr.RPC != "" {
				fmt.Fprintf(w, ", %s", sr.RPC)
			}
			if sr.HTTPStatus != 0 {
				fmt.Fprintf(w, ", HTTP %d", sr.HTTPStatus)
			}
			fmt.Fprintln(w, "): failed —", sr.Reason)
			if sr.BodySample != "" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, "```")
				fmt.Fprintln(w, sr.BodySample)
				fmt.Fprintln(w, "```")
				fmt.Fprintln(w)
			}
		}
		fmt.Fprintln(w, "---")
		fmt.Fprintln(w)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
