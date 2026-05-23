import { FormEvent, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { fermentationClient } from "@/lib/clients";
import {
  AddFermentationLogRequestSchema,
  FermentationStatus,
  UpdateFermentationStatusRequestSchema,
} from "@/gen/stillhouse/v1/fermentation_pb";
import { fermentationStatusLabel, formatQty } from "@/lib/format";

const statusOptions = [
  FermentationStatus.PITCHED,
  FermentationStatus.ACTIVE,
  FermentationStatus.FINISHED,
  FermentationStatus.DISTILLED,
  FermentationStatus.CANCELLED,
];

export function FermentationDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const run = useQuery({
    queryKey: ["getFermentationRun", id],
    queryFn: () => fermentationClient.getFermentationRun({ id: id! }),
    enabled: !!id,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["getFermentationRun", id] });

  const addLog = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddFermentationLogRequestSchema>>) =>
      fermentationClient.addFermentationLog(msg),
    onSuccess: refresh,
  });
  const updateStatus = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateFermentationStatusRequestSchema>>) =>
      fermentationClient.updateFermentationStatus(msg),
    onSuccess: refresh,
  });

  const [sg, setSG] = useState("");
  const [ph, setPH] = useState("");
  const [temp, setTemp] = useState("");
  const [notes, setNotes] = useState("");

  function submitLog(e: FormEvent) {
    e.preventDefault();
    if (!id) return;
    const sgN = sg.trim();
    const phN = ph.trim();
    const tempN = temp.trim();
    addLog.mutate(
      create(AddFermentationLogRequestSchema, {
        fermentationRunId: id,
        specificGravity: sgN ? Number(sgN) : 0,
        specificGravitySet: !!sgN,
        ph: phN ? Number(phN) : 0,
        phSet: !!phN,
        temperatureC: tempN ? Number(tempN) : 0,
        temperatureCSet: !!tempN,
        notes,
      }),
    );
    setSG("");
    setPH("");
    setTemp("");
    setNotes("");
  }

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{r.fermenterLabel}</h1>
          <p className="text-sm text-fg-muted">
            {r.recipeName} · mash{" "}
            <Link to={`/mashes/${r.mashRunId}`} className="underline">
              #{r.mashNo}
            </Link>{" "}
            · pitched {r.pitchAt ? new Date(Number(r.pitchAt.seconds) * 1000).toLocaleString() : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-xs text-fg-muted">Status</label>
          <select
            value={r.status}
            onChange={(e) =>
              updateStatus.mutate(
                create(UpdateFermentationStatusRequestSchema, {
                  id: r.id,
                  status: Number(e.target.value) as FermentationStatus,
                }),
              )
            }
            className="rounded border border-border-strong px-2 py-1 text-sm"
          >
            {statusOptions.map((s) => (
              <option key={s} value={s}>
                {fermentationStatusLabel(s)}
              </option>
            ))}
          </select>
        </div>
      </header>

      <section className="mb-8 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Volume" value={r.initialVolumeLSet ? `${formatQty(r.initialVolumeL)} L` : "—"} />
        <Stat label="Current SG" value={r.currentSpecificGravitySet ? r.currentSpecificGravity.toFixed(4) : "—"} />
        <Stat
          label="Calc. ABV"
          value={r.calculatedAbvPctSet ? `${r.calculatedAbvPct.toFixed(2)}%` : "—"}
          hint="(OG − current SG) × 131.25"
        />
        <Stat label="Logs" value={String(r.logs.length)} />
      </section>

      <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase text-fg-muted">Add reading</h2>
        <form onSubmit={submitLog} className="flex flex-wrap items-end gap-3">
          <NumField label="Specific gravity" value={sg} onChange={setSG} step="0.001" placeholder="1.020" />
          <NumField label="pH" value={ph} onChange={setPH} step="0.1" />
          <NumField label="Temperature °C" value={temp} onChange={setTemp} step="0.1" />
          <TextField label="Notes" value={notes} onChange={setNotes} className="flex-1 min-w-[180px]" />
          <button
            type="submit"
            disabled={addLog.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {addLog.isPending ? "Saving…" : "Add reading"}
          </button>
          {addLog.error && (
            <span className="text-sm text-red-400">
              {addLog.error instanceof ConnectError ? addLog.error.rawMessage : String(addLog.error)}
            </span>
          )}
        </form>
      </section>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3 text-right">SG</th>
              <th className="px-4 py-3 text-right">pH</th>
              <th className="px-4 py-3 text-right">Temp (°C)</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {r.logs.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-3 text-fg-muted">
                  No readings yet.
                </td>
              </tr>
            )}
            {r.logs.map((l) => (
              <tr key={l.id}>
                <td className="px-4 py-3 text-fg-muted">
                  {l.observedAt ? new Date(Number(l.observedAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {l.specificGravitySet ? l.specificGravity.toFixed(4) : "—"}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {l.phSet ? l.ph.toFixed(2) : "—"}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {l.temperatureCSet ? l.temperatureC.toFixed(1) : "—"}
                </td>
                <td className="px-4 py-3 text-fg-muted">{l.notes}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-lg border border-border bg-surface-2 p-4 shadow-sm">
      <p className="text-xs uppercase text-fg-muted">{label}</p>
      <p className="mt-1 text-xl font-semibold text-fg">{value}</p>
      {hint && <p className="mt-1 text-xs text-fg-subtle">{hint}</p>}
    </div>
  );
}

function NumField({
  label,
  value,
  onChange,
  step,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  step?: string;
  placeholder?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input
        type="number"
        step={step}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="w-32 rounded border border-border-strong px-3 py-2 text-sm"
      />
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  className,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-border-strong px-3 py-2 text-sm"
      />
    </div>
  );
}
