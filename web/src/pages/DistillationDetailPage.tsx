import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { Button } from "@/components/Button";
import { useConfirm } from "@/components/ConfirmDialog";
import { CutProfile } from "@/components/CutProfile";
import { RunCurve } from "@/components/RunCurve";
import { PlanCurve } from "@/components/PlanCurve";
import {
  emptyReading,
  readingToRequest,
  SourceBadge,
  StrengthReading,
  StrengthReadingValue,
} from "@/components/StrengthReading";
import { DeleteButton } from "@/components/DeleteButton";
import { LoadError } from "@/components/LoadError";
import { Shell } from "@/components/Shell";
import { useToast } from "@/components/Toast";
import { bulkClient, distillationClient, engineeringClient, fermentationClient } from "@/lib/clients";
import {
  AddDistillationChargeRequestSchema,
  AddDistillationCutRequestSchema,
  AddDistillationReadingRequestSchema,
  DeleteDistillationCutRequestSchema,
  DeleteDistillationReadingRequestSchema,
  DistillationCutKind,
  DistillationStatus,
  RecordProductionGaugeRequestSchema,
  UpdateDistillationCutRequestSchema,
} from "@/gen/stillhouse/v1/distillation_pb";
import { FermentationStatus } from "@/gen/stillhouse/v1/fermentation_pb";
import { writeErrorText } from "@/lib/errors";
import {
  cutKindLabel,
  distillationStatusLabel,
  formatLAA,
  formatQty,
} from "@/lib/format";
import type { DistillationReading } from "@/gen/stillhouse/v1/distillation_pb";
import { PlanRunRequestSchema, type StillSpec } from "@/gen/stillhouse/v1/engineering_pb";

const cutKindOptions = [
  { v: DistillationCutKind.FORESHOTS, label: "Foreshots" },
  { v: DistillationCutKind.HEADS, label: "Heads" },
  { v: DistillationCutKind.HEARTS, label: "Hearts" },
  { v: DistillationCutKind.TAILS, label: "Tails" },
  { v: DistillationCutKind.FEINTS_SAVED, label: "Feints (saved)" },
];

export function DistillationDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const run = useQuery({
    queryKey: ["getDistillationRun", id],
    queryFn: () => distillationClient.getDistillationRun({ id: id! }),
    enabled: !!id,
  });
  const containers = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const ferments = useQuery({
    queryKey: ["listFermentationRuns", "all"],
    queryFn: () => fermentationClient.listFermentationRuns({}),
  });

  const toast = useToast();

  // Everything: containers, lists and pickers elsewhere move with a run.
  const refresh = () => void qc.invalidateQueries();
  const failed = (e: unknown) => toast("danger", writeErrorText(e));

  const addCharge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddDistillationChargeRequestSchema>>) =>
      distillationClient.addDistillationCharge(msg),
    onSuccess: refresh,
  });
  const addCut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddDistillationCutRequestSchema>>) =>
      distillationClient.addDistillationCut(msg),
    onSuccess: refresh,
  });
  const addReading = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddDistillationReadingRequestSchema>>) =>
      distillationClient.addDistillationReading(msg),
    onSuccess: refresh,
  });
  const deleteReading = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DeleteDistillationReadingRequestSchema>>) =>
      distillationClient.deleteDistillationReading(msg),
    onSuccess: refresh,
    onError: failed,
  });
  const deleteCut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DeleteDistillationCutRequestSchema>>) =>
      distillationClient.deleteDistillationCut(msg),
    onSuccess: refresh,
    onError: failed,
  });
  const updateCut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateDistillationCutRequestSchema>>) =>
      distillationClient.updateDistillationCut(msg),
    onSuccess: refresh,
    onError: failed,
  });
  const recordGauge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RecordProductionGaugeRequestSchema>>) =>
      distillationClient.recordProductionGauge(msg),
    onSuccess: (r) => {
      refresh();
      // An instrument past due for calibration doesn't block the gauge, so
      // this is the only place the operator will see it. A warning nobody
      // surfaces is a warning nobody acts on.
      r.warnings.forEach((w) => toast("warning", w));
    },
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (run.error && !run.data) return <Shell><LoadError error={run.error} onRetry={() => void run.refetch()} /></Shell>;
  if (!run.data?.run) return <Shell><p>Not found.</p></Shell>;

  const r = run.data.run;

  const voided = !!r.voidedAt;
  const closed = voided || r.status === DistillationStatus.CANCELLED;

  const totalChargeLAA = r.charges.reduce((s, c) => s + c.laa, 0);
  const totalCutLAA = r.cuts.reduce((s, c) => s + c.laa, 0);
  const gaugeLAA = r.gauge?.laa ?? 0;
  const heartsRecoveryPct = totalChargeLAA > 0 ? (gaugeLAA / totalChargeLAA) * 100 : 0;
  const totalCutRecoveryPct = totalChargeLAA > 0 ? (totalCutLAA / totalChargeLAA) * 100 : 0;

  return (
    <Shell>
      <header className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">
            Distillation #{r.runNo}
            {voided && <span className="ml-3 rounded bg-danger/15 px-2 py-1 align-middle text-sm font-medium text-danger-fg">VOIDED</span>}
          </h1>
          <p className="text-sm text-fg-muted">
            {r.runDate} · {r.stillLabel || "no still labelled"} · {distillationStatusLabel(r.status)}
          </p>
        </div>
        {r.gauge && !voided && (
          <div className="text-right">
            <p className="text-xs text-fg-muted">Gauged into</p>
            <p className="font-medium text-fg">{r.gauge.destinationContainerName}</p>
            <p className="text-3xl font-bold tracking-tight text-fg">{formatLAA(r.gauge.laa)} L LAA</p>
          </div>
        )}
      </header>

      <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Metric label="Charge LAA in" value={`${formatLAA(totalChargeLAA)} L`} />
        <Metric label="Cuts LAA total" value={`${formatLAA(totalCutLAA)} L`} />
        <Metric
          label="Hearts recovery"
          value={r.gauge ? `${heartsRecoveryPct.toFixed(1)}%` : "—"}
          hint={r.gauge ? "gauge ÷ charge" : "no gauge yet"}
          highlight={!!r.gauge}
        />
        <Metric
          label="Total cut recovery"
          value={totalChargeLAA > 0 ? `${totalCutRecoveryPct.toFixed(1)}%` : "—"}
          hint={totalChargeLAA > 0 ? "all cuts ÷ charge" : "no charge yet"}
        />
      </section>

      {voided && r.voidedReason && <p className="mb-6 rounded bg-surface-2 p-4 text-sm text-fg shadow-sm">{r.voidedReason}</p>}

      {r.cutAnalysis && <CutProfile analysis={r.cutAnalysis} cuts={r.cuts} />}

      <RunCurve readings={r.readings} />
      {totalChargeLAA > 0 && (
        <PlanThisRun
          chargeVolumeL={r.charges.reduce((s, c) => s + c.volumeChargedL, 0)}
          chargeAbvPct={(totalChargeLAA / Math.max(1e-9, r.charges.reduce((s, c) => s + c.volumeChargedL, 0))) * 100}
          readings={r.readings}
        />
      )}
      <ReadingsPanel
        runId={r.id}
        readings={r.readings}
        onAdd={(m) => addReading.mutateAsync(m)}
        adding={addReading.isPending}
        addError={addReading.error}
        onDelete={(readingId) => deleteReading.mutate(create(DeleteDistillationReadingRequestSchema, { id: readingId }))}
      />

      <section className="mb-8 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <ChargesPanel
          run={r}
          ferments={(ferments.data?.runs ?? []).filter((f) => f.status !== FermentationStatus.CANCELLED)}
          onSubmit={(m) => addCharge.mutateAsync(m)}
          submitting={addCharge.isPending}
          error={addCharge.error}
          onDelete={(chargeId) => distillationClient.deleteDistillationCharge({ id: chargeId }).then(refresh)}
        />
        <CutsPanel
          run={r}
          onSubmit={(m) => addCut.mutateAsync(m)}
          submitting={addCut.isPending}
          error={addCut.error}
          onDelete={(id) => deleteCut.mutate(create(DeleteDistillationCutRequestSchema, { id }))}
          deleting={deleteCut.isPending}
          onUpdate={(m) => updateCut.mutateAsync(m)}
          updating={updateCut.isPending}
        />
      </section>

      <section className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold text-fg-muted">Production gauge</h2>
        {r.gauge ? (
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <Row k="Destination">{r.gauge.destinationContainerName}</Row>
            <Row k="Gauged at">
              {r.gauge.gaugeDate ? new Date(Number(r.gauge.gaugeDate.seconds) * 1000).toLocaleString() : ""}
            </Row>
            {/* Volume and ABV are the values at 20 °C — what the B266 is
                built from. Where a correction was applied, show what was
                actually read off the instrument beside it. */}
            <Row k="Volume (20 °C)">
              {formatQty(r.gauge.volumeL)} L
              {Math.abs(r.gauge.volumeFactorC - 1) > 1e-9 && r.gauge.observedVolumeL > 0 && (
                <span className="ml-2 text-xs text-fg-subtle">
                  gauged {formatQty(r.gauge.observedVolumeL)} L · ×{r.gauge.volumeFactorC.toFixed(4)}
                </span>
              )}
            </Row>
            <Row k="Strength (20 °C)">{r.gauge.abvPct.toFixed(2)}%</Row>
            <Row k="LAA"><span className="font-semibold text-fg">{formatLAA(r.gauge.laa)} L</span></Row>
            {r.gauge.temperatureCSet && <Row k="Temperature">{r.gauge.temperatureC.toFixed(1)}°C</Row>}
            {r.gauge.observedDensityKgM3Set && (
              <Row k="Hydrometer">{r.gauge.observedDensityKgM3.toFixed(1)} kg/m³</Row>
            )}
            <Row k="Determination"><SourceBadge source={r.gauge.strengthSource} /></Row>
          </dl>
        ) : closed ? (
          <p className="text-sm text-fg-muted">This run is closed and cannot be gauged.</p>
        ) : (
          <GaugeForm
            distillationRunId={r.id}
            containers={containers.data?.containers ?? []}
            onSubmit={(m) => recordGauge.mutate(m)}
            submitting={recordGauge.isPending}
            error={recordGauge.error}
          />
        )}
      </section>
    </Shell>
  );
}

function ChargesPanel({
  run,
  ferments,
  onSubmit,
  submitting,
  error,
  onDelete,
}: {
  run: ReturnType<typeof useDistillationRun>;
  ferments: { id: string; fermenterLabel: string; mashNo: number; recipeName: string }[];
  onSubmit: (m: ReturnType<typeof create<typeof AddDistillationChargeRequestSchema>>) => Promise<unknown>;
  submitting: boolean;
  error: Error | null;
  onDelete: (id: string) => Promise<unknown>;
}) {
  const [fermID, setFermID] = useState("");
  const [vol, setVol] = useState("");
  const [abv, setAbv] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!fermID || !vol || !abv) return;
    onSubmit(
      create(AddDistillationChargeRequestSchema, {
        distillationRunId: run.id,
        fermentationRunId: fermID,
        volumeChargedL: Number(vol),
        abvPct: Number(abv),
        chargeOrder: Math.max(0, ...run.charges.map((c) => c.chargeOrder)) + 1,
      }),
    ).then(
      () => {
        setFermID("");
        setVol("");
        setAbv("");
      },
      () => undefined,
    );
  }
  return (
    <Panel title="Charges">
      {!run.gauge && !run.voidedAt && run.status !== DistillationStatus.CANCELLED && <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <Field label="Ferment" as="select" value={fermID} onChange={setFermID} required>
          <option value="">—</option>
          {ferments.map((f) => (
            <option key={f.id} value={f.id}>
              {f.fermenterLabel} · mash #{f.mashNo} · {f.recipeName}
            </option>
          ))}
        </Field>
        <Field label="Vol (L liquid)" value={vol} onChange={setVol} type="number" step="any" width="w-24" required />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="any" width="w-20" required />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-accent px-3 py-1 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          Add
        </button>
        {error && <span className="text-xs text-danger-fg">{writeErrorText(error)}</span>}
      </form>}
      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-3 py-2">Order</th>
            <th className="px-3 py-2">Ferment</th>
            <th className="px-3 py-2 text-right">Vol (L)</th>
            <th className="px-3 py-2 text-right">ABV</th>
            <th className="px-3 py-2 text-right">LAA</th>
            <th className="px-3 py-2" />
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {run.charges.length === 0 && (
            <tr><td colSpan={6} className="px-3 py-2 text-fg-muted">No charges yet.</td></tr>
          )}
          {run.charges.map((c) => (
            <tr key={c.id}>
              <td className="px-3 py-2 text-fg-muted">{c.chargeOrder}</td>
              <td className="px-3 py-2 text-fg">{c.fermenterLabel} (#{c.mashNo})</td>
              <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{formatQty(c.volumeChargedL)}</td>
              <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{c.abvPct.toFixed(2)}%</td>
              <td className="px-3 py-2 text-right tabular-nums font-semibold text-fg">{formatLAA(c.laa)}</td>
              <td className="px-3 py-2 text-right">
                {!run.gauge && !run.voidedAt && (
                  <DeleteButton
                    title="Remove this charge?"
                    body="The audit trail keeps what was removed."
                    onDelete={() => onDelete(c.id)}
                  />
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

function CutsPanel({
  run,
  onSubmit,
  submitting,
  error,
  onDelete,
  deleting,
  onUpdate,
  updating,
}: {
  run: ReturnType<typeof useDistillationRun>;
  onSubmit: (m: ReturnType<typeof create<typeof AddDistillationCutRequestSchema>>) => Promise<unknown>;
  submitting: boolean;
  error: Error | null;
  onDelete: (id: string) => void;
  deleting: boolean;
  onUpdate: (m: ReturnType<typeof create<typeof UpdateDistillationCutRequestSchema>>) => Promise<unknown>;
  updating: boolean;
}) {
  const confirm = useConfirm();
  // Track which cut id is currently being edited inline. Empty string = none.
  const [editingId, setEditingId] = useState<string>("");
  const [editKind, setEditKind] = useState("");
  const [editVol, setEditVol] = useState("");
  const [editAbv, setEditAbv] = useState("");
  function startEdit(c: { id: string; kind: number; volumeL: number; abvPct: number; cutOrder: number }) {
    setEditingId(c.id);
    setEditKind(String(c.kind));
    setEditVol(String(c.volumeL));
    setEditAbv(String(c.abvPct));
  }
  function commitEdit(cut: { cutOrder: number; observedAt?: Timestamp }) {
    if (!editVol.trim() || !editAbv.trim()) return;
    onUpdate(create(UpdateDistillationCutRequestSchema, {
      id: editingId,
      kind: Number(editKind) as DistillationCutKind,
      volumeL: Number(editVol),
      abvPct: Number(editAbv),
      cutOrder: cut.cutOrder,
      observedAt: cut.observedAt,
    })).then(() => setEditingId(""), () => undefined);
  }
  const [kind, setKind] = useState(String(DistillationCutKind.HEARTS));
  const [vol, setVol] = useState("");
  const [abv, setAbv] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!vol || !abv) return;
    onSubmit(
      create(AddDistillationCutRequestSchema, {
        distillationRunId: run.id,
        kind: Number(kind) as DistillationCutKind,
        volumeL: Number(vol),
        abvPct: Number(abv),
        cutOrder: Math.max(0, ...run.cuts.map((c) => c.cutOrder)) + 1,
      }),
    ).then(
      () => {
        setVol("");
        setAbv("");
      },
      () => undefined,
    );
  }

  return (
    <Panel title="Cuts">
      {!run.voidedAt && <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <Field label="Kind" as="select" value={kind} onChange={setKind}>
          {cutKindOptions.map((k) => (
            <option key={k.v} value={k.v}>{k.label}</option>
          ))}
        </Field>
        <Field label="Vol (L liquid)" value={vol} onChange={setVol} type="number" step="any" width="w-24" required />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="any" width="w-20" required />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-accent px-3 py-1 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          Add
        </button>
        {error && <span className="text-xs text-danger-fg">{writeErrorText(error)}</span>}
      </form>}
      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-3 py-2">#</th>
            <th className="px-3 py-2">Kind</th>
            <th className="px-3 py-2 text-right">Vol (L)</th>
            <th className="px-3 py-2 text-right">ABV</th>
            <th className="px-3 py-2 text-right">LAA</th>
            <th className="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {run.cuts.length === 0 && (
            <tr><td colSpan={6} className="px-3 py-2 text-fg-muted">No cuts yet.</td></tr>
          )}
          {run.cuts.map((c) => {
            const isEditing = editingId === c.id;
            if (isEditing) {
              return (
                <tr key={c.id} className="bg-surface-3">
                  <td className="px-3 py-2 text-fg-muted">{c.cutOrder}</td>
                  <td className="px-3 py-2">
                    <select
                      value={editKind}
                      onChange={(e) => setEditKind(e.target.value)}
                      className="rounded border border-border-strong bg-surface px-2 py-1 text-sm text-fg"
                    >
                      {cutKindOptions.map((k) => (
                        <option key={k.v} value={k.v}>{k.label}</option>
                      ))}
                    </select>
                  </td>
                  <td className="px-3 py-2 text-right">
                    <input
                      type="number" step="0.01"
                      value={editVol}
                      onChange={(e) => setEditVol(e.target.value)}
                      className="w-20 rounded border border-border-strong bg-surface px-2 py-1 text-right text-sm text-fg"
                    />
                  </td>
                  <td className="px-3 py-2 text-right">
                    <input
                      type="number" step="0.01"
                      value={editAbv}
                      onChange={(e) => setEditAbv(e.target.value)}
                      className="w-20 rounded border border-border-strong bg-surface px-2 py-1 text-right text-sm text-fg"
                    />
                  </td>
                  <td className="px-3 py-2 text-right text-fg-subtle">—</td>
                  <td className="px-3 py-2 text-right space-x-2">
                    <button
                      onClick={() => commitEdit(c)}
                      disabled={updating || !editVol.trim() || !editAbv.trim()}
                      className="text-xs text-accent-hover hover:text-accent disabled:opacity-50"
                    >
                      Save
                    </button>
                    <button
                      onClick={() => setEditingId("")}
                      className="text-xs text-fg-muted hover:text-fg"
                    >
                      Cancel
                    </button>
                  </td>
                </tr>
              );
            }
            return (
              <tr key={c.id}>
                <td className="px-3 py-2 text-fg-muted">{c.cutOrder}</td>
                <td className="px-3 py-2 text-fg">{cutKindLabel(c.kind)}</td>
                <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{formatQty(c.volumeL)}</td>
                <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{c.abvPct.toFixed(2)}%</td>
                <td className="px-3 py-2 text-right tabular-nums font-semibold text-fg">{formatLAA(c.laa)}</td>
                <td className="px-3 py-2 text-right space-x-3">
                  {!run.voidedAt && <>
                  <button
                    onClick={() => startEdit(c)}
                    className="px-2 py-2 text-xs text-fg-muted hover:text-fg"
                  >
                    Edit
                  </button>
                  <button
                    onClick={async () => {
                      const ok = await confirm({
                        title: `Delete this ${cutKindLabel(c.kind)} cut?`,
                        body: <>The row vanishes from the run; total cut LAA recalculates from what remains.</>,
                        confirmLabel: "Delete cut",
                        tone: "danger",
                      });
                      if (ok) onDelete(c.id);
                    }}
                    disabled={deleting}
                    className="px-2 py-2 text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                  >
                    Delete
                  </button>
                  </>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Panel>
  );
}

function GaugeForm({
  distillationRunId,
  containers,
  onSubmit,
  submitting,
  error,
}: {
  distillationRunId: string;
  containers: { id: string; name: string; archived: boolean }[];
  onSubmit: (m: ReturnType<typeof create<typeof RecordProductionGaugeRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
  const [destID, setDestID] = useState("");
  const [reading, setReading] = useState<StrengthReadingValue>(emptyReading());
  const [notes, setNotes] = useState("");

  const hasStrength = reading.mode === "density" ? reading.density.trim() !== "" : reading.abv.trim() !== "";

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!destID || !reading.volumeL || !hasStrength) return;
    onSubmit(
      create(RecordProductionGaugeRequestSchema, {
        distillationRunId,
        destinationContainerId: destID,
        volumeL: Number(reading.volumeL),
        notes,
        ...readingToRequest(reading),
      }),
    );
  }

  return (
    <form onSubmit={submit} className="grid grid-cols-2 gap-3 text-sm">
      <div className="col-span-2">
        <label className="mb-2 block text-sm font-medium text-fg-muted">Destination container</label>
        <select
          value={destID}
          onChange={(e) => setDestID(e.target.value)}
          required
          className="w-full rounded border border-border-strong px-3 py-2 text-sm"
        >
          <option value="">Select a container…</option>
          {containers.filter((c) => !c.archived).map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </select>
        <p className="mt-1 text-xs text-fg-muted">
          Recording the gauge creates a BulkMovement into this container and updates its running balance.
        </p>
      </div>
      <div className="col-span-2">
        <StrengthReading value={reading} onChange={setReading} />
      </div>
      <div className="col-span-2">
        <Field label="Notes" value={notes} onChange={setNotes} />
      </div>
      <div className="col-span-2 flex items-center gap-3">
        <Button type="submit" disabled={submitting}>
          {submitting ? "Recording…" : "Record gauge"}
        </Button>
        {error && (
          <span className="text-sm text-danger-fg">
            {writeErrorText(error)}
          </span>
        )}
      </div>
    </form>
  );
}

function Metric({
  label, value, hint, highlight,
}: { label: string; value: string; hint?: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border bg-surface-2 p-4 shadow-sm ${highlight ? "border-success/30" : "border-border"}`}>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-success-fg" : "text-fg"}`}>{value}</p>
      {hint && <p className="mt-1 text-xs text-fg-subtle">{hint}</p>}
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">{title}</h2>
      </header>
      <div className="overflow-x-auto">{children}</div>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between border-b border-border py-2 last:border-0">
      <dt className="text-fg-muted">{k}</dt>
      <dd>{children}</dd>
    </div>
  );
}

type FieldProps = {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  step?: string;
  as?: "input" | "select";
  children?: React.ReactNode;
  width?: string;
  required?: boolean;
};

function Field({ label, value, onChange, type = "text", step, as = "input", children, width, required }: FieldProps) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      {as === "select" ? (
        <select
          required={required}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`max-w-full ${width ?? ""} rounded border border-border-strong px-2 py-1 text-sm`}
        >
          {children}
        </select>
      ) : (
        <input
          type={type}
          step={step}
          required={required}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`${width ?? "w-full"} rounded border border-border-strong px-3 py-2 text-sm`}
        />
      )}
    </div>
  );
}

// Type alias for inferring the run shape from getDistillationRun's response.
type DistillationRunData = NonNullable<
  Awaited<ReturnType<typeof distillationClient.getDistillationRun>>["run"]
>;
function useDistillationRun(): DistillationRunData {
  // Phantom helper to give the inner panel components a precise type
  // for the `run` prop. Not used at runtime.
  throw new Error("type-only helper");
}

/**
 * ReadingsPanel — the run's log: what the instruments said, when. Strength
 * is recorded as read at the parrot, at the parrot's temperature, because
 * that is the figure the cut was made on; the temperature beside it is what
 * lets it be corrected later. Every field is optional: a reading records
 * what was taken.
 */
function ReadingsPanel({
  runId,
  readings,
  onAdd,
  adding,
  addError,
  onDelete,
}: {
  runId: string;
  readings: DistillationReading[];
  onAdd: (m: ReturnType<typeof create<typeof AddDistillationReadingRequestSchema>>) => Promise<unknown>;
  adding: boolean;
  addError: unknown;
  onDelete: (id: string) => void;
}) {
  const [abv, setAbv] = useState("");
  const [spiritTemp, setSpiritTemp] = useState("");
  const [heat, setHeat] = useState("");
  const [potTemp, setPotTemp] = useState("");
  const [vapourTemp, setVapourTemp] = useState("");
  const [notes, setNotes] = useState("");
  const confirm = useConfirm();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const num = (v: string) => (v.trim() === "" ? undefined : Number(v));
    const a = num(abv), st = num(spiritTemp), h = num(heat), pt = num(potTemp), vt = num(vapourTemp);
    onAdd(
      create(AddDistillationReadingRequestSchema, {
        distillationRunId: runId,
        distillateAbvPct: a ?? 0, distillateAbvPctSet: a !== undefined,
        distillateTemperatureC: st ?? 0, distillateTemperatureCSet: st !== undefined,
        heatSettingPct: h ?? 0, heatSettingPctSet: h !== undefined,
        potTemperatureC: pt ?? 0, potTemperatureCSet: pt !== undefined,
        vapourTemperatureC: vt ?? 0, vapourTemperatureCSet: vt !== undefined,
        notes,
      }),
    ).then(
      () => {
        setAbv(""); setSpiritTemp(""); setHeat(""); setPotTemp(""); setVapourTemp(""); setNotes("");
      },
      () => undefined,
    );
  };

  const ordered = [...readings].sort((a, b) => Number(a.observedAt?.seconds ?? 0) - Number(b.observedAt?.seconds ?? 0));

  return (
    <section className="mb-8 rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Readings at the still</h2>
        <p className="text-xs text-fg-subtle">
          Strength as read at the parrot, at its own temperature. The run's log, and what calibrates the planner.
        </p>
      </header>
      <form onSubmit={submit} className="flex flex-wrap items-end gap-3 px-4 py-4">
        <ReadingField label="Parrot %" value={abv} onChange={setAbv} step="0.1" placeholder="72.5" />
        <ReadingField label="Spirit °C" value={spiritTemp} onChange={setSpiritTemp} step="0.1" />
        <ReadingField label="Heat %" value={heat} onChange={setHeat} step="1" placeholder="60" />
        <ReadingField label="Pot °C" value={potTemp} onChange={setPotTemp} step="0.1" />
        <ReadingField label="Vapour °C" value={vapourTemp} onChange={setVapourTemp} step="0.1" />
        <div className="min-w-[180px] flex-1">
          <label className="mb-1 block text-xs text-fg-muted">Notes</label>
          <input value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="the glass, a cut"
                 className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
        </div>
        <Button type="submit" disabled={adding}>{adding ? "Saving…" : "Add reading"}</Button>
        {addError ? (
          <span className="text-sm text-danger-fg">
            {writeErrorText(addError)}
          </span>
        ) : null}
      </form>
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">When</th>
              <th className="px-4 py-2 text-right">Parrot %</th>
              <th className="px-4 py-2 text-right">Spirit °C</th>
              <th className="px-4 py-2 text-right">Heat %</th>
              <th className="px-4 py-2 text-right">Pot °C</th>
              <th className="px-4 py-2 text-right">Vapour °C</th>
              <th className="px-4 py-2">Notes</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {ordered.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-3 text-fg-muted">No readings yet — the first one is usually the first drop.</td>
              </tr>
            )}
            {ordered.map((rd) => (
              <tr key={rd.id}>
                <td className="px-4 py-2 text-fg-muted">
                  {rd.observedAt ? new Date(Number(rd.observedAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-2 text-right">{rd.distillateAbvPctSet ? rd.distillateAbvPct.toFixed(1) : "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{rd.distillateTemperatureCSet ? rd.distillateTemperatureC.toFixed(1) : "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{rd.heatSettingPctSet ? rd.heatSettingPct.toFixed(0) : "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{rd.potTemperatureCSet ? rd.potTemperatureC.toFixed(1) : "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{rd.vapourTemperatureCSet ? rd.vapourTemperatureC.toFixed(1) : "—"}</td>
                <td className="px-4 py-2 text-fg-muted">{rd.notes}</td>
                <td className="px-4 py-2 text-right">
                  <button
                    type="button"
                    className="text-xs text-fg-subtle hover:text-danger-fg"
                    onClick={async () => {
                      if (await confirm({ title: "Delete this reading?", body: "It leaves the run's log and the audit trail keeps what it said." })) {
                        onDelete(rd.id);
                      }
                    }}
                  >
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function ReadingField({
  label, value, onChange, step, placeholder,
}: { label: string; value: string; onChange: (v: string) => void; step: string; placeholder?: string }) {
  return (
    <div className="w-24">
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input type="number" step={step} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}

/**
 * PlanThisRun — the planner's curve for this run's charge, with what the
 * parrot actually read laid over it once there are readings. The plan's
 * clock starts at the charge; the readings start whenever the first one was
 * taken, so the two are lined up at the first drop — the moment both know.
 */
function PlanThisRun({
  chargeVolumeL,
  chargeAbvPct,
  readings,
}: {
  chargeVolumeL: number;
  chargeAbvPct: number;
  readings: DistillationReading[];
}) {
  const presets = useQuery({
    queryKey: ["listStillPresets"],
    queryFn: () => engineeringClient.listStillPresets({}),
  });
  const [still, setStill] = useState<StillSpec | null>(null);
  const [firing, setFiring] = useState("60");
  const chosen = still ?? presets.data?.presets[0] ?? null;

  const plan = useMutation({
    mutationFn: (req: ReturnType<typeof create<typeof PlanRunRequestSchema>>) => engineeringClient.planRun(req),
  });

  const run = () => {
    if (!chosen) return;
    plan.mutate(
      create(PlanRunRequestSchema, {
        still: chosen,
        charge: { volumeL: chargeVolumeL, abvPct: chargeAbvPct },
        settings: { firingPct: Number(firing), refluxRatio: chosen.dephlegmator ? 3 : 0, cutStrengthsPct: [75, 65, 48], stopDistillateAbvPct: 10 },
      }),
    );
  };

  const p = plan.data?.plan;
  let actual: { h: number; v: number }[] | undefined;
  let planned: { h: number; v: number }[] = [];
  let collected: { h: number; v: number }[] = [];
  let cuts: { abv: number; h: number; share: number }[] = [];
  let totalHours = 0;
  if (p) {
    const firstDropH = Number(p.timeToFirstDropS) / 3600;
    const strengthReadings = [...readings]
      .filter((rd) => rd.observedAt && rd.distillateAbvPctSet)
      .sort((a, b) => Number(a.observedAt!.seconds) - Number(b.observedAt!.seconds));
    // Lined up at the first drop: the plan's first drop sits where the first strength was read.
    const t0 = strengthReadings.length ? Number(strengthReadings[0].observedAt!.seconds) : 0;
    const shift = strengthReadings.length ? firstDropH : 0;
    planned = p.points.map((pt) => ({ h: Number(pt.atS) / 3600 - shift, v: pt.distillateAbvPct })).filter((pt) => pt.h >= 0);
    collected = p.points.map((pt) => ({ h: Number(pt.atS) / 3600 - shift, v: pt.collectedLaa })).filter((pt) => pt.h >= 0);
    cuts = p.cuts.map((c) => ({ abv: c.abvPct, h: Number(c.atS) / 3600 - shift, share: c.shareOfCharge }));
    totalHours = Number(p.runTimeS) / 3600 - shift;
    if (strengthReadings.length >= 2) {
      actual = strengthReadings.map((rd) => ({ h: (Number(rd.observedAt!.seconds) - t0) / 3600, v: rd.distillateAbvPct }));
      totalHours = Math.max(totalHours, actual[actual.length - 1].h);
    }
  }

  return (
    <>
      <section className="mb-4 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-surface-2 px-4 py-3 shadow-sm">
        <div>
          <label className="mb-1 block text-xs text-fg-muted">Plan this run on</label>
          <select
            value={chosen?.name ?? ""}
            onChange={(e) => setStill(presets.data?.presets.find((s) => s.name === e.target.value) ?? null)}
            className="rounded border border-border-strong px-2 py-1.5 text-sm"
          >
            {presets.data?.presets.map((s) => (
              <option key={s.name} value={s.name}>{s.name}</option>
            ))}
          </select>
        </div>
        <ReadingField label="Run heat %" value={firing} onChange={setFiring} step="1" />
        <span className="pb-2 text-xs text-fg-subtle">
          {chargeVolumeL.toFixed(0)} L at {chargeAbvPct.toFixed(1)} %
        </span>
        <Button type="button" onClick={run} disabled={plan.isPending || !chosen}>{plan.isPending ? "Planning…" : "Plan"}</Button>
        {plan.error ? (
          <span className="text-sm text-danger-fg">
            {plan.error instanceof ConnectError ? plan.error.rawMessage : String(plan.error)}
          </span>
        ) : null}
      </section>
      {p && (
        <PlanCurve planned={planned} collected={collected} actual={actual} cuts={cuts} totalHours={totalHours} basis={p.basis} />
      )}
    </>
  );
}
