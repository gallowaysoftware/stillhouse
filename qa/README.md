# stillhouse-qa

LLM-assisted QA loop for the Stillhouse app. Rides on the
[vamp](https://github.com/gallowaysoftware/vibe) pipeline runtime —
the same builder fake-crime / iitn use, but the stages target test
discovery, test design, agentic execution, and report generation
rather than audio production.

## Phases

| Phase | Command | Purpose |
|------:|---------|---------|
| 1 | `stillhouse-qa prepare` | LLM researches the Canadian craft-distillery domain (LAA math, Form B266, excise stamps, RLS, audit log) and emits a `primer.md`. |
| 2 | `stillhouse-qa discover` (todo) | Reads the proto + Go sources + primer; emits an entity / workflow / invariant map. |
| 3 | `stillhouse-qa plan` (todo) | Generates a breadth-first test plan from the map. |
| 4 | `stillhouse-qa run` (todo) | Executes the plan against a running Stillhouse stack via ConnectRPC + Playwright. |
| 5 | `stillhouse-qa deepen` (todo) | Iterative deepening: each finding queues follow-up probes until a depth / budget cap is hit. |
| 6 | `stillhouse-qa report` (todo) | Aggregates findings into `qa-report.md`. |

Findings land in `qa/runs/<phase>-<n>/`. Nothing is committed back to
the rest of the repo — the QA loop is read-only from Stillhouse's
perspective.

## Prerequisites

- A running [vibe](https://github.com/gallowaysoftware/vibe) daemon
  with a `long_form` profile pointing at a 27B-class model
  (`qwen3.6-27b-mtp-q6_k` is what the prompts were tuned against).
- A Stillhouse dev stack the `run` phase can probe:
  `make dev-up backend-dev web-dev seed` (each in its own terminal).

## Running

```bash
go install ./cmd/stillhouse-qa
stillhouse-qa prepare --out qa/runs/prepare-001
```

The `prepare` phase finishes in ~90s on a 27B model and produces
~22k chars of structured markdown primer covering LAA math, Form
B266 mechanics, excise stamp lifecycle, Canadian Whisky maturation,
RLS invariants, audit-log completeness, and a prioritized test
surface inventory.

## Why in-tree

Putting the QA loop inside the Stillhouse repo means schema and
source drift can't make the QA agent stale without also breaking
the build it tests. The `qa/` directory has its own `go.mod` so the
vamp dependency tree doesn't bleed into the backend.
