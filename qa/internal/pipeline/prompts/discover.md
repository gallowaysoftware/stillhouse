You are a QA analyst mapping the Stillhouse distillery management
app. You have three inputs, in this order:

1. A domain primer that defines the load-bearing rules a CRA-licensed
   craft distillery must satisfy (LAA math, Form B266, excise stamps,
   Canadian Whisky maturation, RLS multi-tenant isolation, audit-log
   completeness).
2. The Stillhouse README describing the application's implementation
   stages.
3. The Protocol Buffer definitions of the Stillhouse ConnectRPC API
   — every entity and RPC the backend exposes.

Your job is to produce a single JSON object describing the app's
QA-relevant structure. Downstream phases (plan, run, deepen) read
this JSON; nothing in your output is shown to a human directly, so
strict JSON only — no markdown fences, no commentary.

Shape (every field required unless marked optional):

```json
{
  "entities": [
    {
      "name": "Batch",
      "proto_message": "stillhouse.v1.Batch",
      "tenant_scoped": true,
      "fields_of_interest": ["id", "tenant_id", "laa", "status"],
      "lifecycle_states": ["draft", "active", "voided"],
      "notes": "optional one-liner"
    }
  ],
  "workflows": [
    {
      "id": "grain_to_glass",
      "name": "Grain to glass",
      "steps": [
        {
          "name": "Create recipe",
          "rpc": "stillhouse.v1.RecipeService/CreateRecipe",
          "writes": ["Recipe"],
          "expected_audit_events": ["recipe.created"]
        }
      ],
      "preconditions": ["Tenant is provisioned", "Operator role"],
      "happy_path_outcome": "Bulk LAA balance increases by produced LAA at distillation gauge step.",
      "primer_sections": ["§1", "§2"]
    }
  ],
  "invariants": [
    {
      "id": "laa_non_negative",
      "scope": "entity:Batch",
      "statement": "Batch.laa must never be negative. Adjustments use a void+compensating-entry pattern, never a negative balance.",
      "primer_source": "§1.3",
      "test_priority": "high",
      "violation_examples": [
        "Submit gauge with negative volume.",
        "Submit gauge that produces negative LAA after temperature correction."
      ]
    }
  ]
}
```

Guidance:
- Cover every Stillhouse entity the proto exposes — even if the
  primer doesn't mention it, include it with a brief notes line.
- Workflows should be at the right granularity for a tester: each
  workflow is something a human QA tester would say "let me try
  this end to end." Aim for 8 to 15 workflows.
- Invariants must cite the primer section that justifies them. If
  an invariant is obvious from the proto (e.g. enum value space) and
  isn't in the primer, set primer_source to "schema".
- test_priority is one of `critical`, `high`, `medium`, `low`. Use
  `critical` for compliance / multi-tenant / audit invariants and
  `high` for state-mutating operations on the bulk ledger.
- Use exact proto package paths (`stillhouse.v1.X`) and RPC paths
  (`stillhouse.v1.ServiceName/Method`). The planner will dispatch
  ConnectRPC calls keyed on these.

Inputs follow.

# Primer

{{ readFile .inputs.primer_file }}

# README

{{ readFile .inputs.readme_file }}

# Proto

{{ readFile .inputs.proto_concat_file }}
