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
the report but that nothing could ever write. Stage 182 added the third
column of the packaging split. What is left needs something else first:

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

### B4 · B263 — licensed user return — P2

Needed if a tenant holds a user's licence to blend or fortify with imported
spirits. Carries the special duty interaction with `A2`.

### B5 · Branch and division returns — P2

B269 authorization for separate returns per branch or division. `F1` shipped in
stage 170, so there is now something to divide by; what is left is the
authorization itself and a return scoped to one location.

---

## Track C — the measurement and audit chain

The one area where Stillhouse is already ahead of everything commercial. These
finish the job.

---

## Track D — sales and revenue

Competitor parity. Purtrak, Whiskey Systems and Ekos all treat sales as the
spine production hangs from; Stillhouse has no customer concept at all. `D1`
unblocks provincial reporting (`I1`) and the POS loop (`G4`); the order →
pick → removal chain shipped in stage 173.

### D4a · A generated bill of lading — P2

Picking, packing slips, carrier and tracking shipped with the sales chain
(stage 173); the BOL is recorded by reference, not produced. Most carriers
supply their own form, so this is only worth doing for the ones that don't.

### D5 · Keg and returnable container tracking — P2

Shipped in stage 199: the register, the deposit ledger, the fill/return
cycle as an enforced state machine, and freshness. The overlap with marked
special containers turned out to be a *split* rather than an overlap — a
keg at or above 100 L holds one, below it holds packaged spirits, and the
register points at whichever applies without copying any of its figures.

What is left is smaller than the item made it sound:

- ~~Other returnable containers~~ — shipped in stage 212 by widening the
  register rather than adding one, with a `kind` column and a schema
  guard that only a keg may hold spirits.
- Deposit accounting proper. The liability is reported; posting it to the
  licensee's chart of accounts needs a `journal_event_kind` and the
  mapping seam from 000040.

### D7 · Consignment, returns and credits — P2

Returns, restocking and credit notes shipped in stage 198. What is left is
the half that was always going to wait: **the refund itself**. A return
records that duty was paid and remains paid; claiming it back is a B256
under s.181/s.182 and is `A9`, blocked on sourcing that form's rules.

Consignment shipped in stage 210: stock at a customer's premises that is
still ours, not treated as a removal, and excluded from what the plan
considers available.

D7 is done bar the refund, which waits on `A9`.

---

## Track E — purchasing and cost

---

## Track F — planning and operations

### F7 · Forecasting — P2

Finished-goods demand forecasting shipped in stage 201, alongside the
actual-demand plan rather than instead of it, with the method stated and
unset refusing.

Raw-material and WIP followed in stage 204: bottles to make, the alcohol
they need, and the grain to make it — scaled through the recipe a product
is now explicitly linked to, with free and maturing alcohol reported
apart.

Capacity and lead time followed in stage 205: batches become mashes of a
stated vessel, and the bill gets an order-by date from its slowest line.

F7 is done as far as arithmetic goes. What could still be built on it is
scheduling proper — putting those mashes on the calendar against the work
already booked on that vessel, which stage 185's board has the data for
and does not do. That is a different item in shape from forecasting, and
belongs with `F3` rather than here.

---

## Track G — integrations

### G2 · QuickBooks Online sync — P1

Stage 203 shipped the file: importable journal entries, one row per side,
refusing rather than emitting a partial journal. That covers both QBO and
Xero, and for a distillery filing monthly it may well be the whole
requirement — the note above turned out to be right that this was a
transport rather than a design.

What is left is the *live* sync, and it is the part that cannot be
written from here: an OAuth app registered with Intuit, its refresh-token
handling, and the API's own idea of a journal entry. The same wall as
G4's vendor adapters. Worth doing when somebody is running enough volume
that a monthly file is a chore rather than a routine.

Pulling payments back is a separate question and a larger one: it makes
Stillhouse a reader of the accounting system rather than a writer to it,
and the invoice ledger from stage 180 would need to reconcile against it.

### G3 · Xero sync — P2

The file from stage 203 imports into Xero as it stands — same shape, same
refusals. Only the live sync remains, and the same wall: an OAuth app,
registered with Xero this time.

### G4 · POS and e-commerce ingest — P1

The ingest shipped in stage 200: an idempotent batch endpoint, the SKU
map, the pending/rejected queue, and posting to duty-paid removals through
the same `recordRemoval` a hand-keyed one uses.

What is left is the per-vendor adapters — Square, Lightspeed, Shopify,
Commerce7 — and they are the part that cannot be written from here. Each
needs its own OAuth app, webhook signature scheme and line-item shape, and
those are credentials and API contracts rather than logic. Each adapter is
thin: normalise the vendor's payload into `POSSaleLine` and call
`IngestPOSSales`, which is deliberately where all the care already is.

A CSV upload of the same shape would serve every till that has an export
button and no API, and is probably worth more than any single adapter.

### G6 · Public API and webhooks — P2

Webhooks shipped in stage 196: five event kinds, signed deliveries, a
transactional outbox and a delivery log. What is left is the API half of
this item, which is documentation rather than code — the ConnectRPC
surface already IS the API, and the protos are commented. What it lacks is
a stated compatibility promise: which parts a third party may build on,
and what a breaking change would look like. Worth writing before somebody
builds on it, not after.

More event kinds are cheap to add and deliberately were not: an event kind
that exists only to be webhooked is one nobody can explain later.

### G7 · Distribution and logistics integrations — P2

What Ekos connects to that we don't: VIP, iControl, MicroStar, Arryved.
Relevant only at a scale none of our tenants are at yet. Listed so it isn't
rediscovered as a gap.

---

## Track H — platform

### H6 · Full bilingual coverage — P1

Also blocks the Quebec half of invoicing: an invoice must be available in
French (Bill 96 / Charter of the French Language), and stage 180 shipped the
document in English only.


`lib/i18n` works but is imported in 4 of 46 components. Quebec is 73
distilleries, roughly a quarter of the national market, and Bill 96 makes
French-first UI and French invoices a legal requirement for Quebec customers.
Either commit or scope Quebec out explicitly.

### H7 · Multi-entity — P2

Stage 195 shipped the switcher: one email, accounts at several distilleries,
and a move between them that keeps the browser where it is. It re-verifies
against the target account rather than trusting the session, because a
password that is right at one distillery need not be right at another.

What is left is the harder half of this item, and it is a different thing
from switching:

- ~~A group view~~ — shipped in stage 213. Rows per licence, never a
  combined return, and each read performed as the account the caller
  actually holds at that entity.
- ~~Shared reference data~~ — shipped in stage 214, and the answer was
  that it should not be shared. Copying needs no home above the tenant
  and no change to RLS, and it is the correct semantics rather than the
  cheap one: a licence has to own the definitions its own filed figures
  were computed from.

H7 is done.

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

### I2 · Ontario, British Columbia, Alberta — P1

The framework shipped in stage 179; this is the content that goes in it —
each board's report definitions, deadlines and rates, from their own published
material. **Blocked on primary sources.** Stillhouse will not ship a table of
other people's deadlines from memory, and the framework is built so that a
requirement with nothing behind it is marked `unknown` and never goes overdue.


LCBO direct-delivery reporting and mark-up remittance, the Ontario Small
Distillers Direct to Store Delivery programme (under 75,000 L, >50% raw
material fermented on site), AGCO manufacturer obligations, BC LDB and AGLC
equivalents.

### I3 · Quebec, Atlantic, Prairies — P2

SAQ, NSLC, and the remaining boards. Quebec depends on `H6`.

### I4 · Container deposit and stewardship — P2

The periodic report shipped in stage 208: containers into each market,
netted against returns, with each deposit rate's provenance carried
beside the money and anything short of *sourced* excluded from a
remittable total.

What is left is data and one more fee, and the first is the reason the
second waits:

- **Sourced rates.** Every deposit rate in `internal/pricing` is
  `Indicative` today, so no report is remittable yet. Each is one page on
  a programme's own site — Encorp BC, BCMB, ODRP, Consignaction — and the
  report already names which jurisdictions are standing between it and a
  remittance.
- **Stewardship fees**, which are a separate charge from the refundable
  deposit and are not modelled at all. Worth adding as its own `Rate`
  beside `ContainerDepositCAD` when the first sourced deposit rate lands,
  since the two arrive from the same page.

### I5 · Food safety and traceability — P2

Stage 194 shipped the recall simulation and the one-up-one-down walk: from a
material lot forward to the mashes, gauges, packaged lots and removals it
reached, with the exact half and the possible-contact half reported apart.

What is left is narrower:

- ~~Other origins~~ — shipped in stage 211. Not "mostly plumbing" as
  this line guessed: the exact/possible-contact boundary sits in a
  different place for each origin, and the container walk has to be
  recursive because spirit does not move once.
- **A preventive control plan** where SFCR requires one. That is a document
  with a required structure, not a query, and it needs the regulation's own
  wording rather than a paraphrase — same discipline as the excise notices.

---

## Track J — the things nobody else can do

Only worth starting once A–C are done. These are why the product is interesting
rather than merely adequate.

### J1 · Filing readiness assistant — P2

The half that had to be deterministic shipped in stage 191: the return is
compared against the closing balance of the last one filed, a break names
the difference, and the entries most likely to have caused it — dated
inside the filed period, entered after it was filed — are listed. That is
the check that could not be left to a model, because it is arithmetic on a
figure CRA already holds.

What is left is the part a model is actually good at, and it is narrower
than this item originally described:

- Prose over the period that reads the blockers, the losses awaiting
  classification and the continuity break together, and says which one to
  do first. Today each speaks only for itself.
- ~~The same over the MCP surface~~ — shipped in stage 192 as
  `review_filing`, though it orders the work deterministically rather than
  narrating it.

Deliberately still excluded: anything that puts a *number* on the return.
A model may order the work and explain a discrepancy. It may not compute
one.

### J2 · Anonymised cross-tenant benchmarks — P2

The privacy machinery and the first two metrics shipped in stage 202:
opt-in, reciprocity, a k-floor counted in licensees, dominance
suppression, and quartiles rather than extremes. Angel's share and the
hearts cut are live.

Yield per tonne and conversion efficiency followed in stage 209, which
also pulled `ConversionPercent` out of the mash bench so the cohort and
the bench are one arithmetic.

One metric the item named is deliberately not built:

- **Angel's share by shelf height.** The data is on `barrel_attributes`
  (`level_position`), and splitting a cohort by position multiplies the
  number of cohorts while each still needs its own k floor of five
  distinct licensees. With the participant numbers a new network has, the
  splits would all refuse — and a screen of refusals teaches an operator
  the feature is broken rather than that it is careful. Worth doing when
  there are enough participants that the splits survive, and not before.

J2 is otherwise done.

### J3 · Cask ownership programmes — P2

The maturation statement shipped in stage 197, and the two other pieces
this item named turn out to exist already: buyer records are customers,
and duty at bottling is what `A1` made the duty point in stage 145.

What is left is the commercial wrapper, which is genuinely a sales
feature and belongs with track D rather than here:

- Selling a cask — the order, the money, and the contract term.
- A periodic statement *sent* rather than looked up. The document exists;
  what is missing is scheduling and delivery, and stage 196's webhooks
  are the plumbing for the second half of that.

### J4 · Expanded MCP surface — P2

Filing review shipped in stage 192 as `review_filing`, read-only: it orders
the outstanding work rather than doing any of it, and B266 generation stays
in the web UI where it belongs.

What is left of this item is the other two flows named here, and they are
not the same shape as each other:

- ~~Work-order status~~ — shipped in stage 206 as `list_work_orders` and
  `set_work_order_status`. Raising a job stays in the web UI: five fields
  a form asks at once and a chat asks one at a time, badly.
- **Scheduling** — multi-row by nature, and the honest resolution is to
  say so rather than keep the line open. Booking a week's work is a board
  with drag targets, not a conversation. **Closing this bullet as
  deliberately out of scope for the MCP surface**; if it ever belongs
  anywhere it is as a read — "what is on the still on Thursday" — which
  `list_work_orders` already answers.

J4 is done.

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
