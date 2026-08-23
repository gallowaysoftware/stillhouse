# Stillhouse — plan

The backlog. [`README.md`](README.md) records what has *shipped*; this file
records what hasn't.

Items carry a track ID (`A1`, `D3`) rather than a stage number, because stage
numbers are assigned sequentially at commit time and this list will reorder.
When an item ships it gets its `stage N:` commit, its README row, and its line
here is deleted — the plan only ever holds open work.

Severity follows the QA convention:

| | meaning |
|---|---|
| **P0** | Produces a wrong number on a filed return, or exposes us legally. |
| **P1** | A distillery has to keep another system, or an operator can't do their job. |
| **P2** | Real improvement, no one is blocked. |

Tracks A–C are correctness and are ordered. Tracks D–J are breadth and can be
picked up in any order, though the dependency notes are real.

---

## Track A — excise correctness

The return has to be right before anything else matters.

### A2 · The rate table holds one band — P1

Stage 142 made rates date-effective and made the lookup refuse outside what
it can cite, which is the half that stops a wrong number reaching a return.
What remains is data:

- Earlier bands from the EDN notice series (EDN104 and predecessors), so a
  reopened or amended pre-2026 period computes rather than refusing. Until
  then any duty event before 2026-04-01 is refused with a message naming
  the covered span.
- The 2027-04-01 indexation, before it arrives. The current band is marked
  known until that date and Stillhouse stops computing duty on it — loudly,
  which is the intent, but it is a date to put in a calendar.
- The special duty on imported spirits delivered to licensed users
  (Schedule 5, $0.12/LAA), which pairs with page 1 line 6 in `A3` and is
  absent entirely.

Each is a struct literal in `internal/excise/rates.go`; the table test
enforces that bands abut exactly and that every band cites a notice.

### A3 · The three B266 lines still without a path — P1

Stage 146 gave EDM10-1-7 page 3 its bulk vocabulary — imports, receipts
from and deliveries to other spirits licensees and licensed users,
packaged spirits returned to bulk, denaturing to DA and SDA, exports, and
bulk returned to production — along with the four lines that existed on
the report but that nothing could ever write. What is left needs something
else first:

- **Spirits packaged in marked special containers**, on page 3 and as the
  third column of the packaging split. Waits on `B3`: they are packaging,
  not bulk, and need their own model.
- **Page 1 line 6** — imported spirits delivered to licensed users, at the
  Schedule 5 special duty rate. Waits on that rate being sourced; see
  `A2`.
- **Page 1 line 8** — refunds, with an attached B256. Waits on `A9`.

### A9 · No refunds, drawback, or duty-paid returns — P1

No B256 application, and no way to book spirits coming back from the duty-paid
market. That is the mechanism behind every recall, every rejected board
shipment, and every destroyed return. Refund reasons are enumerated (1, 7, 8, 9)
and only one may be used per application.


---

## Track B — the other returns and licence types

Not applicable to a bulk-only, duty-at-packaging operation. Required the moment
a tenant holds a second licence, which most hosted tenants will.

### B2 · B262 — excise warehouse return — P1

A licensee with more than one licence files a separate return for each
(EDM3-1-1 ¶49, EDM8-1-1 ¶56). The A→G inventory walk, non-duty-paid reductions
(registered users, accredited representatives, duty free shops, ships' stores,
licensed users domestic/imported, other excise warehouses, export, returned to
spirits licensee, breakage, other), duty-paid reductions (packaged, marked
special containers, other), and the cross-form handoffs to B266 that ¶39
specifies. `RemovalDestinationKind` already covers most of the reduction rows.

### B3 · Marked special containers — P1

EDM3-8-1. Containers 100–1,500 L marked for delivery to registered users or
bottle-your-own premises. They are *packaging*, they have their own B266 and
B262 lines, they can be unmarked and returned to bulk (s.156), and the keg
channel is a live revenue line for craft distillers. Currently unrepresentable.

Stage 143 split B266's packaging figures by duty treatment (duty-paid against
non-duty-paid); the third column on that line — *packaged in marked special
containers* — waits on this item.

### B4 · B263 — licensed user return — P2

Needed if a tenant holds a user's licence to blend or fortify with imported
spirits. Carries the special duty interaction with `A2`.

### B5 · Branch and division returns — P2

B269 authorization for separate returns per branch or division. `F1` shipped in
stage 170, so there is now something to divide by; what is left is the
authorization itself and a return scoped to one location.

### B6 · Licence security sufficiency — P2

The renewal reminders shipped in stage 162: two-year terms, the 30-day window
and the s.23 security expiry are all alerts now that `B1` gives them a subject.
What is left is the part that is not a reminder — whether the posted security is
*sufficient* to cover amounts owing, which needs the duty liability at a point
in time rather than a date.

---

## Track C — the measurement and audit chain

The one area where Stillhouse is already ahead of everything commercial. These
finish the job.

### C4 · Certificates of age and origin — P2

Several trading partners require a certificate signed by a Canadian official
attesting age and origin for exported spirits (EDM3-1-1 ¶43–46). Age is
computed from original warehousing in small wood to removal for export sale,
and resets on redistillation. Stillhouse already holds the maturation clock —
this is the export packet that uses it.

### C5 · Records retention as a stated policy — P2

Subsection 206(1) requires records sufficient to determine compliance;
six years is the working retention window. For a hosted install that becomes
our commitment to describe: cadence, immutability, legal hold, and what a
restore actually returns.

---

## Track D — sales and revenue

Competitor parity. Purtrak, Whiskey Systems and Ekos all treat sales as the
spine production hangs from; Stillhouse has no customer concept at all. `D1`
unblocks provincial reporting (`I1`) and the POS loop (`G4`); the order →
pick → removal chain shipped in stage 173.

### D3 · Invoicing — P1

Invoice generation from orders, numbering, terms, credit notes, AR ageing.
**Quebec:** invoices must be available in French (Bill 96 / Charter of the
French Language) — depends on `H6`.

### D4a · A generated bill of lading — P2

Picking, packing slips, carrier and tracking shipped with the sales chain
(stage 173); the BOL is recorded by reference, not produced. Most carriers
supply their own form, so this is only worth doing for the ones that don't.

### D8 · Ownership of packaged stock — P1

Stage 173 gave bulk containers an owner; packaged inventory has none, so the
chain from a removal back to whoever owned the spirits stops at the bottling
run. Two consequences, both currently disclosed rather than fixed: cost of
sales values every removal as if the goods were the licensee's (the journal
attaches a warning saying so), and a contract-packaged removal's revenue is a
service fee rather than a sale. Needs ownership to be effective-dated rather
than a current column, or a cask sold in place restates a closed period.

### D5 · Keg and returnable container tracking — P2

Keg registry, deposits, fill/return cycle, freshness. Overlaps with marked
special containers (`B3`) but is a distinct asset-tracking problem.

### D7 · Consignment, returns and credits — P2

Product coming back from the duty-paid market, credit notes, restocking, and
the refund path in `A9`. Small wine licensees have a specific consignment
regime; spirits do not, but returns still happen.

---

## Track E — purchasing and cost

### E7 · Production into work in progress — P2

Stage 178 emits the transfer *out* of WIP at bottling, at the full cost of the
run that drew it. Its twin — spirit gauged into WIP at production — still has
no line, because valuing it means walking forward from the mashes to each gauge
and apportioning a mash that fed several of them. That apportionment is a
convention Stillhouse does not have; 000040's argument stands until it does.

### E5 · Reorder points and low-stock alerts — P2

Minimum levels per material, cover-days from consumption rate, and an alert
before the glass runs out. The stamp panel already computes bottles-per-day
cover — generalise it.

### E6 · Inventory reconciliation and cycle counts — P2

A counted-versus-book workflow that feeds the reason-coded adjustment in `A4`
rather than a silent correction.

---

## Track F — planning and operations

### F3 · Production scheduling — P1

Bottling and distillation scheduled from forecast and actual shipments, with a
combined view of inventory, people and equipment. Historical run durations
inform the estimate. Purtrak's most-cited operational feature.

`F2` shipped in stage 171, so there is a board with dates and owners on it and
work orders record when they actually started and finished — which is where the
historical durations this needs will come from. What is missing is the
forecast, the capacity model (`F4`), and the scheduling view itself.

### F4 · Equipment and still register — P2

Stills, tanks, pumps and the filler as first-class assets. Runs attributed to
equipment, capacity known, maintenance scheduled and logged. Prerequisite for
`F3` doing anything honest about capacity.

### F7 · Forecasting — P2

Raw material, WIP and finished-goods forecasting. Ekos ships it; it is what
makes `F3` more than a calendar.

---

## Track G — integrations

### G2 · QuickBooks Online sync — P1

OAuth, chart-of-accounts mapping, invoices and bills pushed, payments pulled.
Every competitor has it and it is the first question on every evaluation.

Stage 161 shipped `G1` first, deliberately, and it may turn out to be enough:
the account mapping and the event set it needs already exist, so this becomes
a transport rather than a design.

### G3 · Xero sync — P2

Same shape as `G2`. Second because QBO dominates in Canada.

### G4 · POS and e-commerce ingest — P1

Square, Lightspeed, Shopify, Commerce7. A tasting-room sale becomes a duty-paid
removal automatically. This is a compliance feature wearing a sales costume —
every sale keyed by hand is a chance to under-report. The pricing engine
already models `SALES_CHANNEL_ON_SITE_RETAIL`; nothing feeds it.

### G6 · Public API and webhooks — P2

The ConnectRPC surface is already the API — this is documenting it, versioning
it, and adding outbound webhooks so other systems can react.

### G7 · Distribution and logistics integrations — P2

What Ekos connects to that we don't: VIP, iControl, MicroStar, Arryved.
Relevant only at a scale none of our tenants are at yet. Listed so it isn't
rediscovered as a gap.

---

## Track H — platform

### H6 · Full bilingual coverage — P1

`lib/i18n` works but is imported in 4 of 46 components. Quebec is 73
distilleries, roughly a quarter of the national market, and Bill 96 makes
French-first UI and French invoices a legal requirement for Quebec customers.
Either commit or scope Quebec out explicitly.

### H7 · Multi-entity — P2

A group running a brewery and a distillery, or two licences, currently needs
two tenants and two logins. Needs an entity switcher above the tenant.

Half the prerequisite is done: stage 155 made email unique per tenant, so one
person can hold an account at each, and login already asks which one they
mean. What is missing is switching between them without signing out.

### H9 · Billing and self-serve signup — P2

Deliberately low. Invite codes and an e-transfer handle a dozen tenants. Revisit
if it stops being a dozen.

### H13 · Tailwind 4 and the browser floor — P2

The only upgrade left after stage 137, and deliberately deferred. Tailwind 4
claims the `--color-*` custom-property namespace that this app's semantic tokens
are defined in, so the official codemod produces a build that succeeds with exit
code 0 and renders unstyled. The fix is mechanical — rename the raw channel
variables out of the reserved namespace and map `@theme` at them — but it needs
a human looking at the screen, not a green build.

It also raises the floor to Safari 16.4 / Chrome 111. For a tool used on
shop-floor phones that is a product decision, and it is the reason this sits
here rather than in a chore commit.

---

## Track I — provincial and regulatory

The layer Purtrak advertises and we don't have. Keep the refuse-to-guess
discipline throughout: a jurisdiction whose board doesn't publish a rate
produces no number.

### I1 · Provincial reporting framework — P1

The shape before the content: jurisdiction registry, rate provenance (already
modelled in `pricing.proto`), report definitions, period alignment with the
excise clock, and submission-format export. Depends on `D1` for customers and
`F6` for product identifiers.

### I2 · Ontario, British Columbia, Alberta — P1

LCBO direct-delivery reporting and mark-up remittance, the Ontario Small
Distillers Direct to Store Delivery programme (under 75,000 L, >50% raw
material fermented on site), AGCO manufacturer obligations, BC LDB and AGLC
equivalents.

### I3 · Quebec, Atlantic, Prairies — P2

SAQ, NSLC, and the remaining boards. Quebec depends on `H6`.

### I4 · Container deposit and stewardship — P2

Encorp BC, BCMB Alberta, Ontario's programme, Quebec's expanded deposit.
Per-container fees, periodic reporting, and remittance. `pricing.proto` already
has `container_deposit_cad` as a line — nothing reports it.

### I5 · Food safety and traceability — P2

`TraceBottlingRun` is a good base, and stage 167 added the other half a recall
needs: a lot can be held by a named person with a reason, and lab results
attach to the gauge, run or cask they were measured on. What is left is SFCR
one-up-one-down traceability, a preventive control plan where required, and a
recall simulation that runs the trace backwards from a lot code.

---

## Track J — the things nobody else can do

Only worth starting once A–C are done. These are why the product is interesting
rather than merely adequate.

### J1 · Filing readiness assistant — P2

An LLM over the period that names the three things which will make the return
not reconcile and points at the movements responsible. Builds on the MCP
surface that already exists.

### J2 · Anonymised cross-tenant benchmarks — P2

Yield per tonne, angel's share by warehouse position and shelf height, cut
ratios, conversion efficiency. A network effect no single-distillery incumbent
can build. The CIBD-grounded science layer is what makes the numbers comparable
rather than noise. Needs an explicit opt-in and a k-anonymity floor.

### J3 · Cask ownership programmes — P2

Private cask sales as a product: buyer records, maturation statements, duty at
bottling. Depends on `A7`.

### J4 · Expanded MCP surface — P2

Back-office writes currently stay in the web UI because multi-row inputs don't
translate to chat. Revisit for the flows that do — filing review, work-order
status, scheduling.

---

## Track K — confidence

Not features. The reasons to believe the numbers above are right.

---

## Ordering

Track A and track C set out to make the return right before anything else
mattered. Most of that has now shipped, in stages 139–148:

| | |
|---|---|
| `K1` `K2` | the projection is pure and testable, and both handlers that write duty onto a return are covered |
| `A10` | closing balances are as of period end, so a late or amended return reports the period's figures |
| `A2` | rates are date-effective and refuse outside what they can cite |
| `A1` | duty crystallises at packaging for a licensee without a warehouse licence |
| `C1` | every gauge can name the CRA-approved instrument that made it |
| `A4` | a book-versus-physical discrepancy is a reason-coded entry, not a residual in losses |
| `A3` | page 3 has the bulk movement vocabulary the form asks for |
| `A5` | a loss says whether it is relieved or duty-payable |
| `A6` | a period is the one the licensee elected to file, with a due date attached |

What is left in track A is blocked on other items rather than on itself:
the rest of `A3` waits on `B3`, `A2` and `A9`; `A7` is inert until the first
contract fill; `A8` and `A9` are their own features.

Track A and C's ordered work is done. `C2`, the audit binder, shipped in
stage 151 and is what the nine stages before it were for: a period's
figures as filed, the movements behind each line, the determinations and
approved instruments behind each movement, and the trail — in one bundle.

Then breadth. `D1` (customers) and `F1` (locations) unblock the most downstream
work and are worth doing early even though neither is urgent on its own.
