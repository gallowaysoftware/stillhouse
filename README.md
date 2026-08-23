# Stillhouse

Open-source distillery management for Canadian craft distillers.

Stillhouse helps a CRA-licensed spirits producer plan recipes, track
production from grain through bottle, manage province-coded excise stamps,
and generate the values that go on CRA Form B266 each month — all in one
ledger that keeps the operational reality and the compliance reality in
sync.

## What's implemented

Each stage below has its own commit with a verified end-to-end smoke test.

| Stage | Feature |
|------:|---------|
| 1 | Auth + multi-tenant foundation (one tenant = one CRA spirits licence) |
| 2 | Materials + versioned recipes + projected-LAA math |
| 3 | Mash + fermentation operational capture |
| 4 | Distillation + production gauge → bulk alcohol ledger (the bridge) |
| 5 | Barrels + maturation clock + Canadian Whisky eligibility |
| 6 | Products + bottling + province-coded excise stamp lifecycle |
| 7 | Packaging removals + CRA Form B266 generation |
| 8 | Audit log for production gauge / bottling / removal / B266 submit |
| 9 | Live dashboard with LAA + duty rollups |
| 10 | Non-superuser app role so RLS actually enforces |
| 11 | Integration test that verifies tenant isolation |
| 12 | Unit tests for the load-bearing alcohol-math functions |
| 13 | Audit log extended to barrel fill / dump / regauge |
| 105 | MCP server — operate Stillhouse from Claude (phone/desktop) over Streamable HTTP |
| 106 | API token management moves into the web UI |
| 107 | MCP polish from first-pass QA: filter barrels out of bulk lists, FK→NotFound on ferment/mash log, emit empty arrays + zero numerics, prefix Connect code in MCP errors |
| 108 | MCP polish round 2: `get_bulk_container` rejects barrel ids, regauge can't fully drain a non-empty barrel, fermentation reading keeps real `0` measurements, slim write-tool responses |
| 109 | Bottling conserves LAA — debits source by `bottleLAA` rather than physical volume × source ABV, handles implicit dilution at bottling time, rejects bottling stronger than the source |
| 110 | Gin recipe backend — botanical roles, per-version sensory scores (10 axes), NGS input + maceration + distillation method, gin-aware LAA projection |
| 111 | Gin recipe UI — form branches on spirit kind, botanical-role pickers, tasting notes, sensory scoring bench (0–10 on 10 axes), version-compare with quantity & score diff highlighting |
| 112 | MCP gin-bench tools — get_recipe, list_recipe_versions, save_recipe_version_sensory; lets an LLM read the recipe + iteration history and score a fresh tasting from the still floor |
| 113 | Gin-bench QA fixes — list_recipe_versions joins sensory (compare workflow works end-to-end on MCP + web), sensory upsert is partial-update so single-axis tweaks don't null the other 9 |
| 114 | Close P3 backlog — LAA/ABV/duty/CAD rounded at display, sensory bench gated to gin recipes, FillBarrel rejects overfill, write tools emit full schema (no more `_set: true` with no value) |
| 115 | Sensory gin-only gate now surfaces as failed_precondition (was getting swallowed as internal error by the handler's catch-all) |
| 116 | Whisky tasting bench — SWRI Flavour Wheel axes (cereal / estery / floral / peaty / feinty / sulphury / woody / winey) + body / finish / overall; backend + MCP `save_recipe_version_whisky_sensory` + web UI for whisky-family recipes |
| 117 | Strength at 20 °C — gauges resolve through the CRA Canadian Alcoholometric Tables 1980; hydrometer indication + temperature determine both strength and volume, the as-observed reading is kept for audit, and every reading records which determination path produced it |
| 118 | Mash bench — cereal species on materials drives gelatinisation guidance off the real grain bill; flags when maize or rice force a separate cereal cook, checks mash temp / pH / thickness against the amylase bands, computes conversion efficiency from OG, and calculates strike temperature. MCP `get_mash` + `plan_strike` |
| 119 | Reduction calculator — proofing down by volume *or by weight*, with the ethanol/water volume contraction computed from the CRA density column instead of estimated. Weighing is exact (mass is additive); the volume figure carries the correction. On tank pages, the bottling page, and MCP `plan_reduction` |
| 120 | Sensory radar — flavour profiles drawn as shapes on both tasting benches, and two versions overlaid on one plot in version-compare. Hand-rolled SVG, no chart dependency; series colours validated for colour-blind separation against both themes |
| 121 | Angel's share — measured annual loss against the band a cool, humid warehouse should hold to, with the shelf height deciding which way the strength ought to drift. Flags a probable leak, and flags strength moving against its position as a likely gauge error. Dashboard alert + barrel panel |
| 122 | Money is not a quantity — `formatCAD` renders CAD at two decimals with grouping everywhere (duty payable was showing four); plus a real bug where the stagnant-inventory alert compared against the wrong enum value and silently skipped every "Other" container, and the last of the hardcoded palette names |
| 123 | Tests for the arithmetic that had none — pricing, password hashing, and the deposit/blend maths. Caught a rounding bug: the `int(x+0.5)` idiom in the rpc helpers rounds negatives the wrong way, and negatives reach it |
| 124 | Adopt existing stock — bring a working distillery's casks into the ledger from a scale reading and a hydrometer, with no batch history behind them. CRA's Mass/Density Procedure; keeps the cask's real age so adopted whisky doesn't lose its CW eligibility; books as opening inventory, never as production |
| 125 | Cut profile — where a run's alcohol went, as an emphasis chart with hearts against the rest, plus the mass balance (you cannot collect more than you charged) and a strength-falls-through-the-run check. Also stops a repeated fermenter charge reporting as a 500 |
| 126 | Yield sanity — a recipe's projection expressed as L/tonne against what the grain can actually give, so efficiencies left at 1.0 stop producing a confident impossible number. Separates "physically impossible" from "ahead of industry" |
| 127 | Fermentation curve — gravity and temperature over time as two plots sharing an x-axis (never a dual axis), plus phase inference and findings for a stuck ferment, thermal stress, and the pH crash that signals contamination |
| 128 | Vatting planner — what actually comes out when parcels are blended, since a blend is neither the sum of its volumes nor the weighted mean of its strengths, then optionally reduced to bottling strength. Web + MCP `plan_blend` |
| 129 | Pricing by channel — wholesale, on-site retail and export priced separately, with every rate carrying its provenance. Ontario and PEI now REFUSE to price the board channel rather than guessing, because neither publishes a spirits mark-up |
| 130 | Operator-supplied alcoholometric tables — the CRA tables are no longer shipped in the binary. Each install downloads its own copy (Crown material: non-commercial reproduction only), the server reads the ZIP as downloaded, and a missing copy degrades to uncorrected readings with a Settings panel explaining the one-time fix rather than taking anything down |
| 131 | Ledger integrity — QA drove the live system as a sole operator and as a team and found alcohol being created: concurrent withdrawals clobbered each other (8 fills moved 800 L while the tank fell 100), a barrel fill credited at the gauged strength but debited at the tank's, a dump silently deleted what the cask kept, and barrel/blend writes landed in already-filed B266 periods |
| 132 | Say which field and why — the day-one adoption path gets the overfill guard the fill path already had, `extract_pct` stops accepting 78 where 0.78 belongs (it was projecting a 1077% ABV wash), the yield check gains an absolute anchor so it can catch an extract its own ceilings are derived from, and validation errors stop arriving as `internal` |
| 133 | A B266 that closes — the packaged section counted alcohol drawn from the tank rather than alcohol that became bottles, so any loss at the filler pushed the reverse-walked opening balance negative on a first-ever return. Packaging loss becomes its own line, and the two sections now reconcile |
| 134 | B266 periods stop overlapping (two returns could cover the same day and report the same alcohol twice) and duty reports per rate band, so a period holding both >7% and ≤7% spirits reconciles against its own figures instead of stating one rate that doesn't multiply out. Materials take extract and moisture the way a malt spec sheet quotes them |
| 135 | Security review — the tenant export was dumping every tenant's users and CRA licence numbers (neither table is RLS-scoped), password hashes included; `/mcp` sat outside the interceptor chain so a viewer's token could move alcohol; seven RPCs were classified nowhere and so silently owner-only, including the strength widget every operator uses; NaN defeated every range check; and editing a product's ABV was unvalidated, which silently reclassifies its excise band |
| 136 | Committee findings — blending was counted as a B266 receipt with no matching withdrawal, so an internal move pushed the opening balance down; the bottling form told operators it would draw the bottled volume rather than the (smaller) volume actually pulled from a stronger tank; database CHECK violations all surfaced as 500s; and the mash bench answered an all-unknown-cereal bill with 0–0 °C instead of saying it doesn't know. Frontend gets its first tests |
| 137 | Dependency currency — Go 1.27, React 19, Vite 8 (Rolldown), TypeScript 7, react-router 7, and every Go and npm dependency at latest. Node 20 had been EOL since April and was pinned in three files. Codegen tool versions pinned so a developer's regeneration can't drift from what CI expects |
| 138 | Self-service signup worked only where tenant isolation wasn't enforcing — it writes its audit row before any tenant context exists, and `audit_events` forces row-level security, so the insert was refused and the whole signup rolled back behind a 500. Invisible in dev, which connects as the superuser |
| 139 | The B266 projection becomes testable — sixty lines of the highest-consequence arithmetic in the product took a `*sqlcgen.Queries`, so exercising a single line of it meant standing up Postgres and seeding a tenant, which is why both reverse-walk defects it has shipped were found by hand against a live system. Gather and project are now separate; the projection is pure and covered by nine unit tests that run against nothing |
| 140 | The two handlers that write duty onto a return get their first tests — `CreateBottlingRun` and `CreateRemoval` were invoked by no test at all, and the existing DB-backed tests seed through raw sqlc, so they covered the SQL and not the handlers. Nine tests later: removals read stock with no row lock (eight concurrent withdrawals against one lot, same lost update as stage 131), and all five document counters allocate `MAX(n)+1` against a UNIQUE column with nothing serialising them — six simultaneous shipments to six different lots lost four of them behind a 500 |
| 141 | A return filed late reports the period's balances, not today's — the two closing-balance queries summed current values with no date at all, so generating May's return in August reported August's figure as May's closing stock, and because opening is reverse-walked from closing both ends moved together and the arithmetic still tied out. Internally consistent and factually wrong is the worst shape for an error |
| 142 | Excise rates become date-effective — `Owed` took a date and ignored it, so any amended, late or reopened prior-period return priced last year's quantities at today's rate and looked fine doing it. Rates now live in a band table keyed on the duty event date, each band citing the CRA notice it was read from, and the lookup **refuses** outside what it can source rather than extrapolating. A period straddling a 1 April indexation is refused too: the form has one line per rate |
| 143 | Duty crystallises at the right event — a spirits licensee without an excise warehouse licence cannot hold packaged spirits non-duty-paid at all, so duty is payable when they are **packaged**, not when they are sold. Stillhouse computed it in exactly one place, the removal, and so reported duty in the month of sale when CRA expects it in the month of bottling: a timing error on a filed return, carrying interest. The duty point is derived from the licence in the database, not toggled, and a cutover date means nothing already filed moves and no litre is dutied twice or dutied never |
| 144 | Instrument register — CRA requires each **individual** instrument used to determine volume or absolute alcohol content to be approved, and approval attaches to the serial number, not the model (EDM3-1-1 ¶24, EDM1-1-5). Stillhouse recorded how a figure was determined and nothing about what determined it, so the audit chain ran quantity → movement → determination → nothing. Gauges now name the instruments that made them; one that is named but unapproved, suspended or retired is refused, and one merely overdue for calibration warns. The tenant export gains the determination tables it was missing entirely |
| 145 | Inventory adjustments — line D on B266 page 3 is a reason-coded entry reconciling book stock to physical, and Stillhouse had no concept of one. A barrel regauge refused any upward variance outright ("regauges record losses only"), tanks could not be reconciled at all, and a downward variance was booked as evaporation whatever caused it — so a counting error and the angels' share landed on the same line, which under EDM3-4-1 do not carry the same duty treatment. An adjustment now says why, names who, keeps the book figure beside the counted one, and gets its own line in the walk rather than being absorbed into the opening balance |
| 146 | The rest of B266 page 3 — imports, receipts from other spirits licensees and licensed users, packaged spirits returned to bulk, deliveries out, denaturing to DA and SDA, exports, and bulk returned to production. Worse than missing lines: nothing in the application ever created a `transfer_in_bond`, `transfer_out_in_bond`, `destruction` or `loss_unaccounted` movement either, so the report had lines for all four that were structurally always zero. Each reportable movement now names its counterparty and its document, gauges through the instrument register, and the reason-to-wire mapping is exhaustively tested after `opening_inventory` was found missing from it since stage 124 |
| 147 | Losses classified by duty treatment — `bulk_losses_laa` was one number, and under EDM3-4-1 an approved destruction is relieved while spirits that cannot be accounted for are duty-payable. Collapsing the two gives a plausible total and the wrong duty. Three states, not two: **unclassified** is the honest default, because Stillhouse does not know whether a given evaporation loss is relieved and the barrel regauge that wrote it did not ask. A period holding unclassified losses reports that it isn't ready to file, and the worklist to resolve them sits on the return itself, showing what each one costs if ruled dutiable before the decision is made |
| 148 | Reporting periods stop assuming a calendar month — a fiscal month is set by notification (B268) rather than assumed, an authorized licensee may file semi-annually (B284), and the return is due by the last day of the fiscal month **following** the period, a date that existed nowhere in the model. The B266 page defaulted to *this* month: the period still running, whose figures aren't final and which nobody files. It now offers the period the licensee should actually be filing, on their own calendar, with the deadline. Also fixes a bug stage 142 introduced: refusing any period that spans an excise indexation made semi-annual filing impossible, since a January-to-June period contains 1 April by construction |
| 149 | The liability boundary — stage 104 said on screen that Stillhouse never files with CRA; what was missing was the step in between. Marking a period submitted now requires a named person to confirm, line by line, that they checked the figures, that they know Stillhouse files nothing, and that they remain responsible for the return. The wording is served by the server and **stored on the period**, not a flag: a tick box whose text changed in a later release proves nothing about what somebody agreed to two years ago. A `TERMS.md` closes the gap the AGPL leaves — it disclaims the software and says nothing about a hosting relationship |
| 150 | Backups that have actually been restored — the tenant export is data portability, not a backup, and nothing could reconstitute a running install. `deploy/backup.sh` verifies each dump by reading it back before counting it, checksums it on both sides of the container boundary, and refuses to leave plaintext where encryption was asked for. The restore drill restores the newest backup into a throwaway Postgres and compares row counts against the live database — and on its first run it found that a dump will not restore onto a fresh Postgres at all, because roles are cluster-wide and were not in the backup. Recovery targets, residency and the six-year retention window are written down in `docs/operations.md`, drill log included |
| 151 | The audit binder — everything in it already existed in pieces (period-locked snapshots, the audit log, gauge determination paths, the instruments behind them, movement-level detail) and nobody had assembled them, so answering "show me how you arrived at line 3" meant exporting four things and explaining the join by hand. One bundle per period: a print-ready document, ten CSV schedules that resolve every id to a name, and a manifest that hashes the lot. For a submitted period the figures come from the **frozen snapshot**, never recomputed — a binder that recalculated would print today's answer under the heading of what was filed |
| 152 | Row-level security is asserted rather than assumed. Three gaps, measured against a live schema rather than reviewed: `api_tokens` carried `tenant_id` with **neither RLS enabled nor any policy** — ownership was checked in Go only; the two sensory tables enabled RLS but never FORCEd it; and nothing checked any of it. The reason `api_tokens` was left out is real — bearer auth resolves a token hash *before* a tenant context exists, because that lookup is what establishes the tenant — so the table goes under the same policy as everything else and that one query goes through a keyhole: two `SECURITY DEFINER` functions owned by a `NOLOGIN BYPASSRLS` role, and nothing else. The server now refuses to boot when `DATABASE_URL` connects as a superuser, which is the failure that looks like everything working. A schema test enumerates every table carrying `tenant_id` and fails if one is missing enable, force, or a policy |
| 153 | The DB-backed tests stop bypassing the thing they are meant to prove. Every one of them connected as the superuser and drove the handlers through that same connection, so RLS never acted on them: the suite was not isolated from itself — a period one test left behind blocked writes for every other tenant, and `go test ./...` failed on a different test each run depending on which packages raced — and it proved nothing about tenant isolation. Fixtures now seed through the superuser pool and the code under test runs through one that RLS applies to, derived from the same DSN with `SET ROLE` so there is no second password to keep in step, and asserted rather than assumed. Two consequences worth naming: the two tests that actually check the tenant boundary had been **skipping in every run** for want of an environment variable nobody set, and a new handler-level test shows what the old configuration was hiding — under the superuser pool, tenant A reads, lists and archives tenant B's containers |
| 154 | Changing a password takes something away. `ResetPassword` and `ChangeMyPassword` updated the hash and returned — the comment in `user.go` said so outright — while sessions ran seven days with no idle timeout and API tokens had **no expiry column at all**, so an attacker who phished a password and minted a token kept both after the victim did the one thing everybody knows to do. Writing a password now sets a revocation watermark in the same statement, and any session that authenticated before it is dead; the caller's own session is re-stamped so doing the safe thing doesn't sign you out. The check sits in middleware rather than the RPC interceptor, because the audit export, tenant export and B266 binder read the session directly. Tokens get an expiry (90 days by default, `never` an explicit choice the UI argues with) enforced inside the auth keyhole, and a **Revoke all my tokens** action next to the password form. A password *reset* — the flow you reach for when you think you've leaked — revokes tokens without being asked. Driving the live server caught that revocation shipping broken: `api_tokens` is under RLS, so the sweep ran with no tenant context, matched zero rows and reported success |
| 155 | One person, two distilleries. `users.email` was `UNIQUE` across the whole install, so the outside bookkeeper or excise consultant — the person who most wants an account, and the best referral channel there is — could hold exactly one, and the second distillery to invite them got an opaque `internal` error. The constraint becomes `UNIQUE (tenant_id, email)`. What that costs is that an address no longer identifies an account on its own, which matters in one place: login, which runs before a tenant context exists. It now verifies the password against every account holding the address and, when more than one matches, answers with the distilleries rather than a session — safe to name, since the caller just proved they hold all of them. Password reset issues a token per account and names the distillery in each email. `tenants.cra_spirits_licence_number` keeps its install-wide `UNIQUE` on purpose: that number is globally unique in the world, so a collision means one of the two is wrong. What was broken there was the error, not the rule |
| 156 | Releases you can name, and a rollback that has actually been run. A hosted install tracked `main` and there was no way to tell what was running — the operator restarting the stack and the person asking whether a fix had landed both had to reason from a container digest. Builds now carry their version through to `/version`, **Settings → This install** and the first line of the boot log, and a build that reports `dev` says plainly that it isn't a release. `make release VERSION=…` refuses a dirty tree, runs lint plus both test suites, tags, and stamps the image. The rollback story stops being a claim: `TestMigrationsRoundTrip` walks all 35 migrations down to nothing and back up in a throwaway database — and found on its first run that `000010`'s down half **drops a cluster-wide role**, which fails when any other database in the cluster holds grants and would have been worse if it had succeeded. Also fixes the Makefile that never worked: `SHELL := /usr/bin/env bash` is a path with a space in it as far as GNU Make is concerned, so every recipe needing a real shell died |
| 157 | Who the alcohol went to. Stillhouse had no customer concept: a removal named its destination in free text, so the same provincial board appeared under three spellings across three months of returns and nothing could be totalled by buyer. Worse, the destination *kind* — which decides whether duty is charged and which B266 line the movement lands on — was re-chosen by hand every time, next to free text that never had to agree with it. A customer record fixes the classification where the decision belongs: the LCBO is a provincial board and always will be, so a removal to them **cannot be typed as an export by accident** — the request's own destination_kind is ignored when a customer is named. Price lists are dated rather than mutable, and carry money as `NUMERIC` and as a decimal string on the wire, because rendering 34.95 through a double is how a cent goes missing. `destination_name` stays for the one-off and for every removal recorded before today — pointing those at customers who didn't exist when the movement happened would be inventing records |
| 158 | A second factor. Stillhouse authenticated with a password and nothing else, on a system holding the records behind a filed excise return. TOTP is written out rather than imported — the algorithm is forty lines, fully specified, and comes with **published test vectors**, which is a better position than "the library is popular"; `internal/totp` is pinned against RFC 6238 Appendix B and RFC 4226 Appendix D. Enrolment is two steps on purpose: nothing is enforced until the app has produced a matching code, because enrolling in one step means a mistyped secret locks out the person who did everything right. Shared secrets are sealed with AES-256-GCM under an operator-supplied key — a TOTP secret sitting in plaintext in a nightly backup hands over both factors at once — and with no key configured, enrolment **refuses** rather than storing one in the clear. The replay guard records the accepted step, so a code read over a shoulder can't be reused inside its window; ten single-use recovery codes are the way back from a lost phone |
| 159 | A role for the person who files the return. Three roles and none fit the outside bookkeeper or excise consultant: a viewer can't record the filing acknowledgement or rule on a loss's duty treatment, which is most of the engagement, so in practice they get an **owner account** — a real privilege escalation dressed up as convenience, and they're also the best referral channel the product has. `accountant` deliberately doesn't sit on the owner > operator > viewer line: more than a viewer on the compliance surface, less than an operator everywhere else. It records **no gauge, no bottling, no removal** — someone who both books a movement and rules on its treatment is the segregation-of-duties problem the audit trail exists to make visible. The gate gains a second dimension rather than a rank, and the procedure-coverage test now catches a typo in the allow-list, which it did on its first run |
| 160 | A dashboard nobody opens on a Tuesday is not an alert. Stillhouse could work out, at any moment, that a return was due in nine days or that there were four days of stamps left — all of it reachable only by going to look. An alert is now a **condition with a life cycle**, not a message: it opens when the condition becomes true, updates rather than duplicating while it stays true, and resolves itself when it stops. There is no dismiss button; acknowledging says a human has *seen* it, which is a different claim from the condition having gone away, and conflating the two is how alerting systems become things people mute. Five rules — return due, return overdue, stamps below a week of cover, a fermentation that stopped reporting, a cask ungauged for a year — evaluated every fifteen minutes and emailed once each, to whoever opted in and can actually act. The return-due and stamp callouts computed in the browser were deleted in the same change, because the same fact appearing twice is worse than either version alone |
| 161 | The seam that lets Stillhouse not become an accounting package. It knows what happened and what it was worth — duty crystallised on a run, grain in at a lot cost, materials into a mash, stock out — but not which account each belongs in, because that's the licensee's chart of accounts. So the mapping is data the operator supplies and an unmapped event is **reported, not posted somewhere invented**: a journal line in the wrong account reconciles, and then nobody looks again. Same refusal on figures: a material lot with no recorded cost produces a warning and no line, never a zero, because a zero balances perfectly while understating inventory by exactly what the lot cost. Every line carries the basis it was arrived at, in words. The two work-in-progress kinds are deliberately absent — valuing them needs labour and overhead absorption Stillhouse doesn't have (`E4`). The run-cost chain moved to `internal/costing` so the cost screen and the journal cannot give different answers |
| 162 | What the licensee actually holds. The tenant carried two free-text licence numbers and nothing else — enough to print a number on a return and enough for nothing else. Which returns exist follows from which licences are held; so does where the duty point falls; so does whether a renewal reminder is possible at all. The register carries each licence's number, effective and expiry dates, premises, and the s.23 security behind it, and existing tenants are backfilled so nothing that reads a licence number changes behaviour. Licences run two years and CRA wants the renewal 30 days out, so expiry becomes a warning at 60 days and **critical at 30** — past the point where a heads-up is still a heads-up. A licence with no recorded expiry raises nothing on purpose: every CRA licence expires, so a blank means nobody entered it, and a reminder from a guessed date gets believed. The register says how many are blank instead of looking finished |
| 163 | Where every stamp went. Excise stamps are Crown-controlled and the licensee is accountable for each one; Stillhouse tracked three counters per order, which answers "how many are left" and cannot answer the question CRA actually asks — *where did stamp ONT00457 go*. Two things were missing. A **reason**: one void counter lumped together a stamp that jammed in the applicator, a roll that went missing off a bench, and a batch returned to CRA — same arithmetic, completely different events, and only one of them is reportable. And **serials**: the reconciliation now walks an order's issued range end to end and says of every serial which run took it, which disposition claimed it, or that it is still on hand — reporting contiguous runs, overlapping claims (two runs claiming one serial still totals correctly, which is exactly why it's dangerous), and any way the account fails to close, as sentences rather than numbers to notice. Existing voids are backfilled as spoilage, which is what that path was always for |
| 164 | CSV in. Stage 124's adopt-existing-stock path is the careful half of getting a running distillery into Stillhouse — one cask at a time, from a scale and a hydrometer, with the determination trail intact. This is the boring half: the four hundred rows somebody already has in a spreadsheet. Materials, deliveries, products, customers, casks and bottled stock, each with a column list written in the words an operator would use rather than the schema's. The dry run **attempts every write and then abandons it**, which is the only way to catch what validation cannot see — a perfectly well-formed file whose names collide with rows already in the database. Everything lands in one transaction, so a bad row on line 380 leaves nothing behind and there is no rollback step to remember. Casks record their strength as supplied and say so when no temperature was given, rather than implying figures went through the tables when they did not |

**What's next:** [`PLAN.md`](PLAN.md) is the open backlog — the excise
correctness work, the returns beyond B266, and the operational surface
still missing against the commercial alternatives. This table records what
shipped; that file records what hasn't.


**v1 milestone:** *file one real B266 from Stillhouse for a production
month.* Achieved at Stage 7.

## Architecture

- **Backend** — Go ([backend/](backend/)). Single binary that serves a
  ConnectRPC API and the embedded web frontend.
- **Frontend** — React + TypeScript + Vite + Tailwind ([web/](web/)).
  Talks to the backend over ConnectRPC (JSON over HTTP).
- **Schemas** — Protocol Buffers ([proto/](proto/)), compiled with Buf into
  typed Go server stubs and typed TS clients.
- **Database** — PostgreSQL with row-level security keyed off the
  per-request `app.current_tenant_id` GUC. Migrations run as a superuser;
  the application connects as a separate non-super role (`stillhouse_app`)
  so the RLS policies actually enforce. One tenant = one CRA spirits licence.
- **Mash guidance** — gelatinisation ranges, amylase activity windows, pH
  and thickness bands come from the IBD/CIBD distilling curriculum and are
  cited on the constant that carries them
  ([backend/internal/mashing](backend/internal/mashing/)). Where the
  curriculum gives no figure for a cereal, Stillhouse reports it as unknown
  rather than interpolating.
- **Alcoholometry** — strength and volume are resolved to 20 °C against the
  CRA [Canadian Alcoholometric Tables 1980](https://www.canada.ca/en/revenue-agency/services/tax/technical-information/excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html),
  computed from the OIML general formula (International Recommendation
  No. 22, 1972). The tables are Crown material and are **not** shipped —
  you download them from CRA once and point the server at the file (see
  [deploy/README.md](deploy/README.md)); until you do, readings record
  uncorrected and nothing else changes. With a copy present the package
  replays all 117,137 published rows back through the lookup, and CRA's
  own worked examples, as tests.
- **License** — AGPL-3.0. Free to self-host, forever. See
  [LICENSE](LICENSE), [NOTICE](NOTICE) for third-party material, and
  [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR.

## Repo layout

```
proto/                Protocol Buffer definitions
backend/              Go server
  cmd/server/         binary entrypoint
  cmd/seed/           bootstrap a starter tenant + admin user
  internal/audit/     audit log writer
  internal/auth/      Argon2id password hashing
  internal/config/    env config
  internal/db/        pgx pool, migrations, sqlc-generated queries
  internal/distilling/ projection math (mass → sugar → ethanol → LAA)
  internal/excise/    Canadian excise duty rates + Owed() helper
  internal/genpb/     generated Go from .proto
  internal/rpc/       ConnectRPC service implementations
  internal/server/    HTTP server + session middleware + static embedding
  internal/tenantdb/  WithTenantTx — opens a tx, sets the tenant GUC,
                      runs your callback with a tenant-scoped Queries
web/                  React frontend
  src/gen/            generated TS Connect client from .proto
  src/pages/          per-route views (Materials, Recipes, Mashes, …,
                      Bulk, Barrels, Products, Stamps, Bottling,
                      Removals, B266 returns, Audit log)
deploy/               Dockerfile + dev compose
.github/workflows/    CI
```

## First-time setup

```sh
# Install required tools (Go binaries: buf, sqlc, golang-migrate, protoc plugins).
make tools

# Start a local Postgres in podman/docker.
make dev-up

# Apply migrations (creates the schema + the stillhouse_app role).
make migrate-up

# Seed a tenant + admin user. Prints the random password — capture it.
make seed

# In two terminals:
make backend-dev    # Go server on :8080, connected as stillhouse_app
make web-dev        # Vite dev server on :5173 (proxies API to :8080)
```

Then open http://localhost:5173 and log in with the seeded credentials.

### Running the integration test

The RLS isolation test exercises two real tenants through both the
admin pool (to insert fixtures) and the app pool (to verify each
tenant sees only its own rows). Requires `make dev-up`.

```sh
make test-integration
```

## MCP server

Stillhouse exposes a [Model Context Protocol](https://modelcontextprotocol.io)
server at `/mcp`, so an LLM (e.g. Claude on your phone) can read the
ledger and capture activity while you have wet hands at the still.

It reuses the same RPC service implementations as the web UI, so RLS
tenant isolation, audit-log writes, and role gating all behave
identically — an MCP-driven barrel fill leaves the same trail as a
web-driven one.

### Issue a token

Tokens are per-user. The plaintext value is printed once; only its
SHA-256 hash is stored.

```sh
make mcp-token EMAIL=you@example.com NAME="phone"
```

### Connect from a client

Configure a remote MCP server pointing at your Stillhouse install:

- **URL:** `https://stillhouse.example.com/mcp`
- **Header:** `Authorization: Bearer sh_…` (the token printed above)

### Tools

- **Read** — `get_dashboard`, `list_bulk_containers`, `get_bulk_container`,
  `list_barrels`, `get_barrel`, `list_recent_bulk_movements`,
  `list_recipes`, `list_products`, `list_b266_periods`
- **Capture** — `fill_barrel`, `regauge_barrel`, `dump_barrel`,
  `add_fermentation_reading`, `add_mash_reading`
- **Bench** — `get_mash` (grain bill + readings + mash guidance),
  `plan_strike` (strike temperature for a target rest),
  `plan_reduction` (proofing down, by volume or by weight),
  `plan_blend` (vatting parcels together, optionally reduced)

Barrel fill / regauge / dump and the production gauge accept a hydrometer
indication (`density_kg_m3`) plus `temperature_c`; supply both and the
strength and volume are resolved to 20 °C through the published CRA tables
rather than taken as typed.

Back-office work (distillation runs with cuts, bottling, removals,
B266 generation) stays in the web UI — those flows have multi-row
inputs that don't translate cleanly to a chat interface.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The short version: cite your
sources for anything that produces a number a distiller relies on, don't
invent domain constants, and keep alcohol conserved.

First pull request? A bot will ask you to sign the [CLA](CLA.md) — one
comment, once. Stillhouse stays AGPL-3.0 regardless; the CLA exists so
the project can also be offered commercially, and it doesn't affect your
copyright in your own work.

Security issues: [SECURITY.md](SECURITY.md) — please don't open a public
issue.

## License

Stillhouse is licensed under the
[GNU Affero General Public License v3.0](LICENSE).

You can run it, read it, modify it and self-host it for free, forever.
The AGPL's network clause means that if you modify Stillhouse and offer
it to others over a network, you have to offer them the source of your
modified version too.

[NOTICE](NOTICE) records third-party material. Note that the CRA's
Canadian Alcoholometric Tables are deliberately *not* included — each
operator supplies their own copy — which keeps commercial redistribution
of Stillhouse itself clear of the Government of Canada's non-commercial
reproduction terms.
