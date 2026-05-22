import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { bulkClient, distillationClient, fermentationClient } from "@/lib/clients";
import {
  AddDistillationChargeRequestSchema,
  AddDistillationCutRequestSchema,
  DistillationCutKind,
  RecordProductionGaugeRequestSchema,
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
  const recordGauge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RecordProductionGaugeRequestSchema>>) =>
      distillationClient.recordProductionGauge(msg),
    onSuccess: refresh,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Distillation #{r.runNo}</h1>
          <p className="text-sm text-stone-500">
            {r.runDate} · {r.stillLabel || "no still labelled"} · {distillationStatusLabel(r.status)}
          </p>
        </div>
        {r.gauge && (
          <div className="text-right">
            <p className="text-xs uppercase text-stone-500">Gauged into</p>
            <p className="font-medium text-stone-900">{r.gauge.destinationContainerName}</p>
            <p className="text-2xl font-semibold text-stone-900">{formatLAA(r.gauge.laa)} L LAA</p>
          </div>
        )}
      </header>

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
        />
      </section>

      <section className="rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Production gauge</h2>
        {r.gauge ? (
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <Row k="Destination">{r.gauge.destinationContainerName}</Row>
            <Row k="Gauged at">
              {r.gauge.gaugeDate ? new Date(Number(r.gauge.gaugeDate.seconds) * 1000).toLocaleString() : ""}
            </Row>
            <Row k="Volume">{formatQty(r.gauge.volumeL)} L</Row>
            <Row k="ABV">{r.gauge.abvPct.toFixed(2)}%</Row>
            <Row k="LAA"><span className="font-semibold text-stone-900">{formatLAA(r.gauge.laa)} L</span></Row>
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
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-stone-200 px-4 py-3">
        <Field label="Ferment" as="select" value={fermID} onChange={setFermID}>
          <option value="">—</option>
          {ferments.map((f) => (
            <option key={f.id} value={f.id}>
              {f.fermenterLabel} · mash #{f.mashNo} · {f.recipeName}
            </option>
          ))}
        </Field>
        <Field label="Vol (L)" value={vol} onChange={setVol} type="number" step="0.1" width="w-24" />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" width="w-20" />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-stone-900 px-3 py-1 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
        >
          Add
        </button>
        {error && <span className="text-xs text-red-600">{error instanceof ConnectError ? error.rawMessage : String(error)}</span>}
      </form>
      <table className="min-w-full divide-y divide-stone-200 text-sm">
        <thead className="text-left text-xs uppercase text-stone-500">
          <tr>
            <th className="px-3 py-2">Order</th>
            <th className="px-3 py-2">Ferment</th>
            <th className="px-3 py-2 text-right">Vol (L)</th>
            <th className="px-3 py-2 text-right">ABV</th>
            <th className="px-3 py-2 text-right">LAA</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-stone-100">
          {run.charges.length === 0 && (
            <tr><td colSpan={5} className="px-3 py-2 text-stone-500">No charges yet.</td></tr>
          )}
          {run.charges.map((c) => (
            <tr key={c.id}>
              <td className="px-3 py-2 text-stone-600">{c.chargeOrder}</td>
              <td className="px-3 py-2 text-stone-900">{c.fermenterLabel} (#{c.mashNo})</td>
              <td className="px-3 py-2 text-right text-stone-600">{formatQty(c.volumeChargedL)}</td>
              <td className="px-3 py-2 text-right text-stone-600">{c.abvPct.toFixed(2)}%</td>
              <td className="px-3 py-2 text-right text-stone-600">{formatLAA(c.laa)}</td>
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
}: {
  run: ReturnType<typeof useDistillationRun>;
  onSubmit: (m: ReturnType<typeof create<typeof AddDistillationCutRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
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
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2 border-b border-stone-200 px-4 py-3">
        <Field label="Kind" as="select" value={kind} onChange={setKind}>
          {cutKindOptions.map((k) => (
            <option key={k.v} value={k.v}>{k.label}</option>
          ))}
        </Field>
        <Field label="Vol (L)" value={vol} onChange={setVol} type="number" step="0.01" width="w-24" />
        <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" width="w-20" />
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-stone-900 px-3 py-1 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
        >
          Add
        </button>
        {error && <span className="text-xs text-red-600">{error instanceof ConnectError ? error.rawMessage : String(error)}</span>}
      </form>
      <table className="min-w-full divide-y divide-stone-200 text-sm">
        <thead className="text-left text-xs uppercase text-stone-500">
          <tr>
            <th className="px-3 py-2">#</th>
            <th className="px-3 py-2">Kind</th>
            <th className="px-3 py-2 text-right">Vol (L)</th>
            <th className="px-3 py-2 text-right">ABV</th>
            <th className="px-3 py-2 text-right">LAA</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-stone-100">
          {run.cuts.length === 0 && (
            <tr><td colSpan={5} className="px-3 py-2 text-stone-500">No cuts yet.</td></tr>
          )}
          {run.cuts.map((c) => (
            <tr key={c.id}>
              <td className="px-3 py-2 text-stone-600">{c.cutOrder}</td>
              <td className="px-3 py-2 text-stone-900">{cutKindLabel(c.kind)}</td>
              <td className="px-3 py-2 text-right text-stone-600">{formatQty(c.volumeL)}</td>
              <td className="px-3 py-2 text-right text-stone-600">{c.abvPct.toFixed(2)}%</td>
              <td className="px-3 py-2 text-right text-stone-600">{formatLAA(c.laa)}</td>
            </tr>
          ))}
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
        <label className="mb-1 block text-xs font-medium text-stone-600">Destination container</label>
        <select
          value={destID}
          onChange={(e) => setDestID(e.target.value)}
          required
          className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
        >
          <option value="">Select a container…</option>
          {containers.filter((c) => !c.archived).map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </select>
        <p className="mt-1 text-xs text-stone-500">
          Recording the gauge creates a BulkMovement into this container and updates its running balance.
        </p>
      </div>
      <Field label="Volume (L)" value={vol} onChange={setVol} type="number" step="0.01" />
      <Field label="ABV %" value={abv} onChange={setAbv} type="number" step="0.01" />
      <Field label="Temperature (°C, optional)" value={temp} onChange={setTemp} type="number" step="0.1" />
      <Field label="Notes" value={notes} onChange={setNotes} />
      <div className="col-span-2 flex items-center gap-3">
        <button
          type="submit"
          disabled={submitting}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
        >
          {submitting ? "Recording…" : "Record gauge"}
        </button>
        {error && (
          <span className="text-sm text-red-600">
            {error instanceof ConnectError ? error.rawMessage : String(error)}
          </span>
        )}
      </div>
    </form>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
      <header className="border-b border-stone-200 bg-stone-50 px-4 py-3">
        <h2 className="text-sm font-semibold uppercase text-stone-500">{title}</h2>
      </header>
      <div className="overflow-x-auto">{children}</div>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between border-b border-stone-100 py-2 last:border-0">
      <dt className="text-stone-500">{k}</dt>
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
      <label className="mb-1 block text-xs text-stone-500">{label}</label>
      {as === "select" ? (
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`${width ?? ""} rounded border border-stone-300 px-2 py-1 text-sm`}
        >
          {children}
        </select>
      ) : (
        <input
          type={type}
          step={step}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`${width ?? "w-full"} rounded border border-stone-300 px-3 py-2 text-sm`}
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
