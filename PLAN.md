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

### A7 · Spirits held for others aren't modelled — P1

EDM10-1-7 page 3: report all bulk spirits *in your possession regardless of who
owns them*, and do not report spirits you own but don't hold. Ownership is not
a property of a bulk container today, so a contract distiller's B266 is wrong
in both directions. Inert until the first contract fill or private cask sale,
then immediately P0 — build it before that day, not during it.

### A8 · Redistillation has no path — P1

EDM3-1-1 ¶38–41. Bulk returned to production for redistilling, and packaged
spirits unpackaged back to bulk, are both reportable movements with a specified
cross-form handoff. Neither exists. Also needs the records showing quantity
taken, quantity produced after, and losses incurred in the process.

### A9 · No refunds, drawback, or duty-paid returns — P1

No B256 application, and no way to book spirits coming back from the duty-paid
market. That is the mechanism behind every recall, every rejected board
shipment, and every destroyed return. Refund reasons are enumerated (1, 7, 8, 9)
and only one may be used per application.


---

## Track B — the other returns and licence types

Not applicable to a bulk-only, duty-at-packaging operation. Required the moment
a tenant holds a second licence, which most hosted tenants will.

### B1 · Licence register on the tenant — P1

Today the tenant has two free-text licence-number fields. Replace with a
register of held licences — spirits (L63A), excise warehouse (L63W), user's
licence, wine — each with its number, effective and expiry dates (licences run
two years and must be renewed 30 days out), and premises. This is the record
that drives which returns exist, what the duty point is (`A1`), and what the
renewal reminders are. Everything else in this track depends on it.

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

B269 authorization for separate returns per branch or division. Depends on
locations (`F1`).

### B6 · Licence renewal and security tracking — P2

Two-year licence terms, renewal 30 days before expiry, and the spirits licence
security requirement ($5,000–$2M, sufficient to cover amounts owing).

The alerting machinery to carry these shipped in stage 160 — a rule here is a
function returning `[]alerting.Alert` and a value on the `alert_kind` enum. What
is missing is the subject: there is no licence register to expire, which is `B1`.

---

## Track C — the measurement and audit chain

The one area where Stillhouse is already ahead of everything commercial. These
finish the job.

### C3 · Stamp serial reconciliation — P1

Stamps are Crown-controlled and must be accounted for. Orders, received,
applied and voided are tracked per jurisdiction, but there is no serial-range
reconciliation down to individual bottling runs and no lost / stolen / damaged
reporting path. Stamps going missing is a thing CRA asks about.

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
and `D6` unblock provincial reporting (`I1`) and the POS loop (`G4`).

### D2 · Sales orders — P1

Order header and lines against products, with status, reserved stock against
packaged inventory, and backorder handling.

### D3 · Invoicing — P1

Invoice generation from orders, numbering, terms, credit notes, AR ageing.
**Quebec:** invoices must be available in French (Bill 96 / Charter of the
French Language) — depends on `H6`.

### D4 · Shipments, picking and documents — P1

Pick lists, packing slips, bills of lading, carrier and tracking. Ekos and
Purtrak both ship picking; Purtrak recently rebuilt theirs.

### D5 · Keg and returnable container tracking — P2

Keg registry, deposits, fill/return cycle, freshness. Overlaps with marked
special containers (`B3`) but is a distinct asset-tracking problem.

### D6 · Removal generated from shipment — P1

The item that makes the whole track pay for itself on the compliance side: a
shipment produces the removal, rather than an operator remembering to record
one. Closes the loop from order to B266 with no re-keying. Depends on `D2`,
`D4`, and `A1` for the duty point.

### D7 · Consignment, returns and credits — P2

Product coming back from the duty-paid market, credit notes, restocking, and
the refund path in `A9`. Small wine licensees have a specific consignment
regime; spirits do not, but returns still happen.

---

## Track E — purchasing and cost

### E1 · Suppliers and purchase orders — P1

Supplier records, POs with lines, approval flow, expected dates, open-PO view.
`RecordMaterialReceipt` currently stands alone with no order behind it.

### E2 · Receiving against a PO, and GRNI — P1

Receipt matched to PO line, partial receipts, goods received not yet invoiced.

### E3 · Landed cost — P1

Freight, duty and handling absorbed into the unit cost of a material lot
rather than sitting in an expense account. Purtrak added this explicitly.

### E4 · Full COGS — P1

`BottlingRunCost` and `ProductCostSummary` cover direct materials and honestly
flag runs with missing prices. Missing: labour, overhead absorption, WIP
valuation, and finished-goods inventory value for the balance sheet.

Stage 161 pulled the chain walk into `internal/costing` so the cost screen and
the accounting journal share one answer, and left the two WIP journal kinds
unemitted rather than inventing a valuation — those are this item.

### E5 · Reorder points and low-stock alerts — P2

Minimum levels per material, cover-days from consumption rate, and an alert
before the glass runs out. The stamp panel already computes bottles-per-day
cover — generalise it.

### E6 · Inventory reconciliation and cycle counts — P2

A counted-versus-book workflow that feeds the reason-coded adjustment in `A4`
rather than a silent correction.

---

## Track F — planning and operations

### F1 · Locations within a tenant — P1

One tenant is one licence with no location dimension inside it. This is a
compliance limit as much as a convenience one: an excise warehouse licence can
specify multiple premises, and the 30% single-retail-store supply rule
(EDM8-1-1 ¶20) is computed per premises. Also blocks `B5` and multi-site
inventory.

### F2 · Work orders and task assignment — P1

Production activities assigned by role, user and due date, with status. What a
second employee needs before the system is usable by a team rather than an
owner.

### F3 · Production scheduling — P1

Bottling and distillation scheduled from forecast and actual shipments, with a
combined view of inventory, people and equipment. Historical run durations
inform the estimate. Purtrak's most-cited operational feature.

### F4 · Equipment and still register — P2

Stills, tanks, pumps and the filler as first-class assets. Runs attributed to
equipment, capacity known, maintenance scheduled and logged. Prerequisite for
`F3` doing anything honest about capacity.

### F5 · Lab, QC and batch release — P1

Methanol and congener results, water chemistry, allergen statements, attached
to a gauge or a run. A release sign-off that gates removal. Nothing in the
category does this well, and it pairs naturally with `C1`.

### F6 · SKU registry and label data — P1

Products carry name, size, ABV and label notes. Missing: GTIN/UPC, case
configuration, board product numbers (CSPC and provincial equivalents),
standards-of-identity declarations under Division 2 of the Food and Drug
Regulations, and the alcohol-container information CRA requires under s.87
(EDM3-2-3). Needed by `D1`, `I2` and any e-commerce listing.

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

### G5 · Barcode and label printing — P1

Barrel tags, case labels, scan-to-regauge, scan-to-pick. How a rackhouse is
actually operated; finding cask 0417 in a list is not it.

### G6 · Public API and webhooks — P2

The ConnectRPC surface is already the API — this is documenting it, versioning
it, and adding outbound webhooks so other systems can react.

### G7 · Distribution and logistics integrations — P2

What Ekos connects to that we don't: VIP, iControl, MicroStar, Arryved.
Relevant only at a scale none of our tenants are at yet. Listed so it isn't
rediscovered as a gap.

---

## Track H — platform

### H3 · Bulk importer — P1

Stage 124's adopt-existing-stock path is the clever half — real casks in from a
scale reading and a hydrometer, no invented history, CW eligibility preserved.
This is the boring half: CSV in for materials, products, barrels, packaged
inventory and customers, with a dry-run and a rollback. The difference between
a distillery trying Stillhouse and finishing.

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

### H10 · Float display on the wire — P2

Known issue F17: LAA, volume and duty carry full IEEE-754 noise
(`0.8399999999999999`) and are rounded only at display. Decide whether the wire
format should carry decimals instead.


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

`TraceBottlingRun` is a good base. SFCR one-up-one-down traceability, a
preventive control plan where required, and a recall simulation that runs the
trace backwards from a lot code.

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

### K3 · `_pct` means two different scales — P2

`abv_pct` is 0–100. `extract_pct`, `moisture_pct` and the three recipe
efficiencies are fractions in [0,1]. Same suffix, same product, hundredfold
apart — and `MashEfficiency.pct` (a percentage) sits beside
`RecipeVersion.mash_efficiency_pct` (a fraction), the same concept at two
scales.

Range validators now catch the ×100 direction, because 78 is out of range for a
fraction. They cannot catch the ÷100 direction, because 0.40 is a legal
percentage — and that is the direction that understates duty. Renaming the
fraction fields `_fraction` is a day's mechanical work and removes the
ambiguity where it actually lives, which is the name a human reads. Named
`Fraction`/`Percent` types in the domain packages would promote the existing
doc comments to compile errors.

### K4 · Pricing provenance is collected and unreachable — P2

`Rate.AsOf` is written by all 17 jurisdiction entries and read by nothing;
`Rate.Source` likewise. `pricing.proto` has no field to carry either. The
provenance discipline — every rate says where it came from and when — was built
in the domain model and never wired to the API, which is where an operator
would judge whether a number is trustworthy. Either surface both fields or
delete them; seventeen hand-maintained values that nothing reads is the worst of
the three options.

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
