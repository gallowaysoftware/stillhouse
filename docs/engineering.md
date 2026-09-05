# Engineering

The engineering section is a flow chart of the distillery — the process
units and the streams between them — with a model behind each unit that
says what it will do before you do it, and learns from what you recorded
after. Planning a distillery is drawing the chart; running one is reading it.

Where this came from: `../distillery-sim`, a game that became a simulator.
Its `docs/design/01-science-m1..m3-*.md` are the CIBD Modules 1–3 written as
simulation specifications, and its `src/DistillerySim.Sim` is the reference
implementation of the still model below. Both are cited from here rather
than copied.

---

## The chart

**Nodes** are the pieces of equipment Stillhouse already knows, given an
engineering specification each, plus the things between them:

| Node | Spec it carries | Model behind it |
|---|---|---|
| Mash tun | volume, heating, jacket | `internal/mashing` (strike, gelatinisation, conversion) |
| Fermenter | volume, jacket or none | `internal/fermenting` (curve, phase, findings) |
| Still | pot volume; heating — electric elements (count × W) or steam; plates and whether a dephlegmator sits above them; condenser | `internal/stillsim` (below) |
| Receivers | foreshots, heads, hearts, tails, feints | the cut envelope from the still |
| Bulk container, barrel | volume, climate | `internal/maturation` (angel's share by climate), regauge prediction |
| Reduction and bottling | — | `internal/alcoholometry` (reduction, blend) |
| Heat source | electric supply (kW available) or boiler (kW, pressure) | capacity |
| Cooling | source (mains, tote, chiller), flow, inlet temperature | condenser duty, tote warming |

**Edges** are streams: volume and strength at 20 °C, litres of absolute
alcohol, temperature, and for utilities energy and water. A batch — a
recipe, or a fermentation that exists — is pushed through the chart and
every edge shows its figure. Per week, the chart shows throughput against
each node's capacity, as counts: *three spirit runs a week on the 200 L;
the fourth waits.* Never a ratio the operator has to compute.

The operator lays the chart out; positions are saved with the graph. A node
opens an inspector for its spec and, for a still, the run planner.

---

## The still model

State is the charge — mass of water and mass of ethanol, and its heat
content — and the still: pot volume, heating, effective equilibrium stages,
condenser capacity, heat loss to the room. The model steps through a run:

1. Heat in from the elements at the firing setting (or from steam at the
   jacket), less losses, raises the charge to its bubble point.
2. At the bubble point, surplus heat becomes vapour. The vapour's strength
   is the equilibrium table applied once for a plain pot and more times for
   a taller still or a column: **plates × tray efficiency, plus what the
   dephlegmator's reflux adds** (CIBD Module 2 — distillation theory: tray
   efficiency 50–80 %, 65 % typical; reflux 2–3 for a floral spirit).
3. The condensate accumulates; the distillate strength, cumulative LAA, pot
   strength and pot temperature are read at every step; the run stops at a
   stated distillate strength.

Outputs: the strength-versus-time curve, cumulative LAA, the **cut
envelope** — the time and cumulative LAA at which the distillate passes each
strength the operator names — hearts yield at those cuts, run time, energy
and cooling water per run.

Sources, on the constant that carries each figure:

| Figure | Source |
|---|---|
| Ethanol–water vapour–liquid equilibrium and boiling points at 1 atm | Carey & Lewis, as tabulated in Perry's; the curriculum's worked figures (x = 0.55 → y ≈ 0.68; 8 % boils at ~94 °C) fall on it |
| Ethanol–water density at 20 °C by mass | CRC — the sim's mass-to-volume table. **Not** the CRA Canadian Alcoholometric Tables, which stay operator-supplied and are used only where a strength is determined |
| Latent and specific heats | CIBD Module 3 — steam theory, heat transfer |
| Effective stages: squat pot 1.0–1.2, tall 1.3–1.8, boil ball +0.1–0.2, plates at 65 % | CIBD Module 2 — batch distillation, distillation theory |
| The spirit-run curve to hold: 48 % distillate near 80 % of the charge's alcohol; the run ends near 98 % | CIBD Module 2 — batch distillation, the worked run |

**What the model does not predict.** Congener and sensory outcomes. The
curriculum gives direction — harder firing carries heavier congeners; a
worm tub keeps sulphur that a shell-and-tube removes — and no figure, so
the planner states the direction and stops. The cut by nose stays the
distiller's; the planner gives the strength and time envelope around it.

**Kyle's stills are the first presets**: a 200 L stainless still,
direct-fired by two 5.5 kW elements, run as a pot or with four plates under
a dephlegmator; and a 30 L still on one 2 kW element, a pot for spirit
runs. Direct electric heat has no steam loop: the heat-in control is the
element setting, the heat-out control is the cooling water.

---

## Calibration

An uncalibrated still plans from its specification and says so on the
curve: *estimated from the still's specification; log a run to calibrate.*
Three things are fitted per still from logged runs, and nothing else:

| Parameter | What it is fitted from |
|---|---|
| Effective stages | the shape of the distillate-strength curve against cumulative LAA |
| Heat loss and element efficiency | time from charge to first drop, and the steady boil-off rate |
| Condenser adequacy | distillate temperature against cooling flow |

The data is **run readings**: during a distillation, the time, the
distillate strength and its temperature at the parrot, the element or steam
setting, and the pot temperature if the still has one. Entered on the run
page or, at the still, through MCP as `add_distillation_reading`, the way
fermentation readings already are. A calibration records the runs it used
and the date; the planner then says *calibrated on three runs, last
2026-10-02, fit within 1.8 points*, and shows the residuals. A fit that does
not converge refuses and says why.

---

## Where it lives

- `backend/internal/stillsim` — the model, cited; tests hold the curriculum
  anchors and the reference sim's numbers.
- `backend/internal/engineering` — the graph, the balances, capacity counts.
- `proto/stillhouse/v1/engineering.proto` — `EngineeringService`:
  `GetGraph`, `SaveGraph`, `SaveStillSpec`, `PlanRun`, `CalibrateStill`;
  `distillation.proto` gains `AddDistillationReading` and the readings on
  `DistillationRun`.
- Migrations: `still_specs` keyed by equipment, `distillation_readings`,
  `engineering_graphs` with nodes, edges and positions.
- MCP: `plan_run`, `add_distillation_reading`.
- Web: `/engineering` — the chart on a node-graph canvas; node inspectors;
  `RunPlanner` with the curves drawn as `FermentationCurve` draws, in SVG;
  on the distillation page, planned against actual once readings exist.

Stages, one commit each: the still model with its anchors and the two
presets; run readings; the run planner on a still; calibration; the chart
and its persistence; balances across the chart; the maturation planner on
barrels; then the mash and fermenter nodes on the packages that exist.
