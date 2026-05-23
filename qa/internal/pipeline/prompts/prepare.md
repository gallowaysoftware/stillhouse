You are an expert in Canadian craft-distillery regulatory compliance,
writing a domain primer for an automated QA agent that will test
Stillhouse — a multi-tenant distillery management app for CRA-licensed
spirits producers.

Your job is to produce a comprehensive markdown primer covering the
load-bearing rules the QA agent must encode as invariants when it
generates tests. Be specific. The agent does not need history or
context — it needs the literal rules, formulas, and edge cases.

Cover the following areas with at least the depth listed. If you are
uncertain about a specific number, name the source you'd expect the
implementation to read it from rather than inventing a value.

## 1. LAA math (litres of absolute alcohol)

- Define LAA precisely. Show the formula relating bulk volume,
  measured ABV (alcohol by volume) at a reference temperature, and
  LAA.
- Cover temperature correction: Canadian customs gauges measure at
  15.6 °C / 60 °F — what happens if the spirit is measured at a
  different temperature?
- Explain when LAA is "projected" vs "actual" in a production
  workflow (recipe planning → mash → fermentation → distillation →
  barrel fill → bottling), and which transitions reduce LAA
  irrevocably (e.g. angel's share, blending dilution).
- Edge cases: zero ABV, > 100% impossible inputs, negative deltas, what
  rounding precision is canonical.

## 2. Excise duty + Form B266

- What Form B266 is, who files it, and the filing cadence.
- The line items on B266 and the inputs each one comes from in the
  ledger (bulk produced, bulk removed for packaging, bulk removed
  duty-free for samples/research, packaged removals by province,
  losses, etc.).
- Excise duty rate structure: how it is computed per LAA, which
  exemptions exist, and what counts as a taxable removal.
- Critical invariant: the sum of all removals in a month must
  reconcile to the bulk-alcohol ledger movements that source them.
  Describe the canonical reconciliation.

## 3. Excise stamp lifecycle (province-coded)

- What a province-coded excise stamp is, who issues them, and why
  province coding matters.
- The lifecycle states a stamp passes through (allocated → on hand →
  affixed to bottle → bottle removed → counted on B266), and what
  invariants hold at each transition.
- Stamp accounting invariants: a stamp can only be affixed to one
  bottle, a bottle can only carry one stamp, a stamp cannot be
  removed once affixed (only voided + destroyed with a witness).
- Province mismatch rules: can a stamp issued for one province
  legally end up affixed to a bottle removed to a different
  province? Under what conditions?

## 4. Canadian Whisky maturation rules

- Minimum maturation period and the cask requirements (size, prior
  use, geography).
- What changes during maturation that the ledger must track:
  evaporation (angel's share), strength shifts, the maturation
  clock start event.
- Edge cases: can maturation pause? What counts as "Canadian Whisky"
  on the label vs the general spirit ledger?

## 5. Multi-tenant isolation (PostgreSQL Row-Level Security)

- A Stillhouse tenant is a single CRA spirits licence. The DB
  enforces tenant isolation via RLS keyed on a session GUC.
- Invariants the QA agent must verify:
  - No query made as tenant A returns rows owned by tenant B,
    regardless of the API call path used.
  - Audit log entries are scoped to the tenant that produced them.
  - Cross-tenant joins are impossible even with SQL injection.
  - The migration / superuser context is the ONLY path that can
    legitimately read across tenants.

## 6. Audit log completeness

- Every state-mutating call (production gauge, bottling run, removal,
  B266 submit, barrel fill / dump / regauge) must have a
  corresponding audit log entry.
- What fields the audit entry must contain (actor, tenant, action,
  before / after state, timestamp, idempotency key if applicable).
- Invariant: it is impossible to alter the bulk alcohol ledger
  without leaving an audit trail. Voids must create a compensating
  entry, never delete history.

## 7. Test surface inventory

Briefly enumerate the kinds of test cases the QA agent should
prioritize as a result of the rules above: happy path per workflow,
boundary inputs (zero, max, negative attempts), invariant-violation
attempts (over-bottle a barrel, use a wrong-province stamp),
multi-tenant probes (call API as tenant A using a tenant B id),
audit-log gap detection, B266 reconciliation drift, idempotency on
retry, race conditions on concurrent gauge / fill.

---

Output: a single markdown document with these seven sections, in
order, using clear headers and short bullet points. Aim for ~3,000
to ~6,000 words. Do not include preamble, apology, or meta commentary.
Start with `# Stillhouse QA Primer` and end with the test surface
inventory section.
