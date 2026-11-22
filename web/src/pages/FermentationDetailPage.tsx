import { FormEvent, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";

import { useConfirm } from "@/components/ConfirmDialog";
import { DeleteButton } from "@/components/DeleteButton";
import { FermentationCurve } from "@/components/FermentationCurve";
import { LoadError } from "@/components/LoadError";
import { Shell } from "@/components/Shell";
import { useToast } from "@/components/Toast";
import { fermentationClient } from "@/lib/clients";
import {
  AddFermentationLogRequestSchema,
  FermentationStatus,
  UpdateFermentationStatusRequestSchema,
} from "@/gen/stillhouse/v1/fermentation_pb";
import { writeErrorText } from "@/lib/errors";
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
  const toast = useToast();
  const confirm = useConfirm();

  const run = useQuery({
    queryKey: ["getFermentationRun", id],
    queryFn: () => fermentationClient.getFermentationRun({ id: id! }),
    enabled: !!id,
  });
  // Everything: the lists and pickers elsewhere show this run too.
  const refresh = () => void qc.invalidateQueries();

  const addLog = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddFermentationLogRequestSchema>>) =>
      fermentationClient.addFermentationLog(msg),
    onSuccess: refresh,
  });
  const updateStatus = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateFermentationStatusRequestSchema>>) =>
      fermentationClient.updateFermentationStatus(msg),
    onSuccess: refresh,
    onError: (e) => toast("danger", writeErrorText(e)),
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
      {
        onSuccess: () => {
          setSG("");
          setPH("");
          setTemp("");
          setNotes("");
        },
      },
    );
  }

  async function setStatus(runId: string, status: FermentationStatus) {
    if (status === FermentationStatus.CANCELLED) {
      const ok = await confirm({
        title: "Cancel this fermentation?",
        body: "It leaves the open runs. Its readings are kept.",
        confirmLabel: "Cancel it",
        tone: "danger",
      });
      if (!ok) return;
    }
    updateStatus.mutate(create(UpdateFermentationStatusRequestSchema, { id: runId, status }));
  }

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (run.error && !run.data) return <Shell><LoadError error={run.error} onRetry={() => void run.refetch()} /></Shell>;
  if (!run.data?.run) return <Shell><p>Not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">{r.fermenterLabel}</h1>
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
            onChange={(e) => void setStatus(r.id, Number(e.target.value) as FermentationStatus)}
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

      <FermentationCurve
        logs={r.logs}
        analysis={r.analysis}
        targetFG={r.targetFinalGravitySet ? r.targetFinalGravity : undefined}
      />

      <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold text-fg-muted">Add reading</h2>
        <form onSubmit={submitLog} className="flex flex-wrap items-end gap-3">
          <NumField label="Specific gravity" value={sg} onChange={setSG} step="0.0001" placeholder="1.020" />
          <NumField label="pH" value={ph} onChange={setPH} step="0.01" />
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
            <span className="text-sm text-danger-fg">
              {writeErrorText(addLog.error)}
            </span>
          )}
        </form>
      </section>

      <div className="overflow-x-auto rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3 text-right">SG</th>
              <th className="px-4 py-3 text-right">pH</th>
              <th className="px-4 py-3 text-right">Temp (°C)</th>
              <th className="px-4 py-3">Notes</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {r.logs.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-3 text-fg-muted">
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
                <td className="px-4 py-3 text-right">
                  <DeleteButton
                    title="Delete this reading?"
                    body="The audit trail keeps what it said."
                    onDelete={() => fermentationClient.deleteFermentationLog({ id: l.id }).then(refresh)}
                  />
                </td>
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
      <p className="text-xs text-fg-muted">{label}</p>
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
