import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { useConfirm } from "@/components/ConfirmDialog";
import { Shell } from "@/components/Shell";
import { bulkClient, distillationClient, fermentationClient } from "@/lib/clients";
import {
  AddDistillationChargeRequestSchema,
  AddDistillationCutRequestSchema,
  DeleteDistillationCutRequestSchema,
  DistillationCutKind,
  RecordProductionGaugeRequestSchema,
  UpdateDistillationCutRequestSchema,
} from "@/gen/stillhouse/v1/distillation_pb";
import {
  cutKindLabel,
  distillationStatusLabel,
  formatLAA,
  formatQty,
} from "@/lib/format";

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

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["getDistillationRun", id] });
    qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    qc.invalidateQueries({ queryKey: ["listRecentBulkMovements"] });
  };

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
  const deleteCut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DeleteDistillationCutRequestSchema>>) =>
      distillationClient.deleteDistillationCut(msg),
    onSuccess: refresh,
  });
  const updateCut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateDistillationCutRequestSchema>>) =>
      distillationClient.updateDistillationCut(msg),
    onSuccess: refresh,
  });
  const recordGauge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RecordProductionGaugeRequestSchema>>) =>
      distillationClient.recordProductionGauge(msg),
    onSuccess: refresh,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Not found.</p></Shell>;

  const r = run.data.run;

  const totalChargeLAA = r.charges.reduce((s, c) => s + c.laa, 0);
  const totalCutLAA = r.cuts.reduce((s, c) => s + c.laa, 0);
  const gaugeLAA = r.gauge?.laa ?? 0;
  const heartsRecoveryPct = totalChargeLAA > 0 ? (gaugeLAA / totalChargeLAA) * 100 : 0;
  const totalCutRecoveryPct = totalChargeLAA > 0 ? (totalCutLAA / totalChargeLAA) * 100 : 0;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Distillation #{r.runNo}</h1>
          <p className="text-sm text-fg-muted">
            {r.runDate} · {r.stillLabel || "no still labelled"} · {distillationStatusLabel(r.status)}
          </p>
        </div>
        {r.gauge && (
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

      <section className="mb-8 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <ChargesPanel
          run={r}
          ferments={ferments.data?.runs ?? []}
          onSubmit={(m) => addCharge.mutate(m)}
          submitting={addCharge.isPending}
          error={addCharge.error}
        />
        <CutsPanel
          run={r}
          onSubmit={(m) => addCut.mutate(m)}
          submitting={addCut.isPending}
          error={addCut.error}
          onDelete={(id) => deleteCut.mutate(create(DeleteDistillationCutRequestSchema, { id }))}
          deleting={deleteCut.isPending}
          onUpdate={(m) => updateCut.mutate(m)}
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
            <Row k="Volume">{formatQty(r.gauge.volumeL)} L</Row>
            <Row k="ABV">{r.gauge.abvPct.toFixed(2)}%</Row>
            <Row k="LAA"><span className="font-semibold text-fg">{formatLAA(r.gauge.laa)} L</span></Row>
            {r.gauge.temperatureCSet && <Row k="Temperature">{r.gauge.temperatureC.toFixed(1)}°C</Row>}
          </dl>
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
}: {
  run: ReturnType<typeof useDistillationRun>;
  ferments: { id: string; fermenterLabel: string; mashNo: number; recipeName: string }[];
  onSubmit: (m: ReturnType<typeof create<typeof AddDistillationChargeRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
  const [fermID, setFermID] = useState("");
  const [vol, setVol] = useState("");
  const [abv, setAbv] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!fermID || !vol) return;
    onSubmit(
      create(AddDistillationChargeRequestSchema, {
        distillationRunId: run.id,
        fermentationRunId: fermID,
        volumeChargedL: Number(vol),
        abvPct: abv ? Number(abv) : 0,
        chargeOrder: run.charges.length + 1,
      }),
    );
    setFermID("");
    setVol("");
    setAbv("");
  }
  return (
    <Panel title="Charges">
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <Field label="Ferment" as="select" value={fermID} onChange={setFermID}>
          <option value="">—</option>
          {ferments.map((f) => (
            <option key={f.id} value={f.id}>
              {f.fermenterLabel} · mash #{f.mashNo} · {f.recipeName}
            </option>
          ))}
        </Field>
        <Field label="Vol (L liquid)" value={vol} onChange={setVol} type="number" step="0.1" width="w-24" />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" width="w-20" />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-accent px-3 py-1 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          Add
        </button>
        {error && <span className="text-xs text-danger-fg">{error instanceof ConnectError ? error.rawMessage : String(error)}</span>}
      </form>
      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-3 py-2">Order</th>
            <th className="px-3 py-2">Ferment</th>
            <th className="px-3 py-2 text-right">Vol (L)</th>
            <th className="px-3 py-2 text-right">ABV</th>
            <th className="px-3 py-2 text-right">LAA</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {run.charges.length === 0 && (
            <tr><td colSpan={5} className="px-3 py-2 text-fg-muted">No charges yet.</td></tr>
          )}
          {run.charges.map((c) => (
            <tr key={c.id}>
              <td className="px-3 py-2 text-fg-muted">{c.chargeOrder}</td>
              <td className="px-3 py-2 text-fg">{c.fermenterLabel} (#{c.mashNo})</td>
              <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{formatQty(c.volumeChargedL)}</td>
              <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{c.abvPct.toFixed(2)}%</td>
              <td className="px-3 py-2 text-right tabular-nums font-semibold text-fg">{formatLAA(c.laa)}</td>
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
  onSubmit: (m: ReturnType<typeof create<typeof AddDistillationCutRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
  onDelete: (id: string) => void;
  deleting: boolean;
  onUpdate: (m: ReturnType<typeof create<typeof UpdateDistillationCutRequestSchema>>) => void;
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
  function commitEdit(cutOrder: number) {
    onUpdate(create(UpdateDistillationCutRequestSchema, {
      id: editingId,
      kind: Number(editKind) as DistillationCutKind,
      volumeL: Number(editVol),
      abvPct: Number(editAbv),
      cutOrder,
    }));
    setEditingId("");
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
        cutOrder: run.cuts.length + 1,
      }),
    );
    setVol("");
    setAbv("");
  }

  return (
    <Panel title="Cuts">
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <Field label="Kind" as="select" value={kind} onChange={setKind}>
          {cutKindOptions.map((k) => (
            <option key={k.v} value={k.v}>{k.label}</option>
          ))}
        </Field>
        <Field label="Vol (L liquid)" value={vol} onChange={setVol} type="number" step="0.01" width="w-24" />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" width="w-20" />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-accent px-3 py-1 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          Add
        </button>
        {error && <span className="text-xs text-danger-fg">{error instanceof ConnectError ? error.rawMessage : String(error)}</span>}
      </form>
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
                      onClick={() => commitEdit(c.cutOrder)}
                      disabled={updating}
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
                  <button
                    onClick={() => startEdit(c)}
                    className="text-xs text-fg-muted hover:text-fg"
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
                    className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                  >
                    Delete
                  </button>
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
  const [vol, setVol] = useState("");
  const [abv, setAbv] = useState("");
  const [temp, setTemp] = useState("");
  const [notes, setNotes] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!destID || !vol || !abv) return;
    onSubmit(
      create(RecordProductionGaugeRequestSchema, {
        distillationRunId,
        destinationContainerId: destID,
        volumeL: Number(vol),
        abvPct: Number(abv),
        temperatureC: temp ? Number(temp) : 0,
        temperatureCSet: !!temp,
        notes,
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
      <Field label="Volume (L liquid)" value={vol} onChange={setVol} type="number" step="0.01" />
      <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" />
      <Field label="Temperature (°C, optional)" value={temp} onChange={setTemp} type="number" step="0.1" />
      <Field label="Notes" value={notes} onChange={setNotes} />
      <div className="col-span-2 flex items-center gap-3">
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {submitting ? "Recording…" : "Record gauge"}
        </button>
        {error && (
          <span className="text-sm text-danger-fg">
            {error instanceof ConnectError ? error.rawMessage : String(error)}
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
};

function Field({ label, value, onChange, type = "text", step, as = "input", children, width }: FieldProps) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      {as === "select" ? (
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`${width ?? ""} rounded border border-border-strong px-2 py-1 text-sm`}
        >
          {children}
        </select>
      ) : (
        <input
          type={type}
          step={step}
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
