import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { barrelClient, bulkClient } from "@/lib/clients";
import {
  BarrelEventKind,
  DumpBarrelRequestSchema,
  FillBarrelRequestSchema,
  RegaugeBarrelRequestSchema,
  VoidBarrelEventRequestSchema,
} from "@/gen/stillhouse/v1/barrel_pb";
import { formatLAA, formatQty } from "@/lib/format";

const eventKindLabels: Record<BarrelEventKind, string> = {
  [BarrelEventKind.UNSPECIFIED]: "—",
  [BarrelEventKind.FILL]: "Fill",
  [BarrelEventKind.REGAUGE]: "Regauge",
  [BarrelEventKind.SAMPLE]: "Sample",
  [BarrelEventKind.DUMP]: "Dump",
  [BarrelEventKind.MOVE]: "Move",
  [BarrelEventKind.DESTROY]: "Destroy",
};

export function BarrelDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const detail = useQuery({
    queryKey: ["getBarrel", id],
    queryFn: () => barrelClient.getBarrel({ id: id! }),
    enabled: !!id,
  });
  const containers = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["getBarrel", id] });
    qc.invalidateQueries({ queryKey: ["listBarrels"] });
    qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    qc.invalidateQueries({ queryKey: ["listRecentBulkMovements"] });
  };

  const fill = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof FillBarrelRequestSchema>>) =>
      barrelClient.fillBarrel(msg),
    onSuccess: refresh,
  });
  const regauge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RegaugeBarrelRequestSchema>>) =>
      barrelClient.regaugeBarrel(msg),
    onSuccess: refresh,
  });
  const dump = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DumpBarrelRequestSchema>>) =>
      barrelClient.dumpBarrel(msg),
    onSuccess: refresh,
  });
  const voidEvent = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidBarrelEventRequestSchema>>) =>
      barrelClient.voidBarrelEvent(msg),
    onSuccess: refresh,
  });

  function onVoidEvent(eventId: string, kind: BarrelEventKind) {
    const reason = window.prompt(
      `Void this ${eventKindLabels[kind]} event? Reverses the linked bulk movement and recomputes both containers' balances. Regauge events can't be voided — record a new corrective regauge instead.`,
      "recorded in error",
    );
    if (!reason || !reason.trim()) return;
    voidEvent.mutate(create(VoidBarrelEventRequestSchema, { id: eventId, reason: reason.trim() }));
  }

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (detail.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!detail.data?.barrel) return <Shell><p>Not found.</p></Shell>;
  const b = detail.data.barrel;

  const nonBarrelContainers =
    containers.data?.containers.filter((c) => !c.archived && c.id !== b.id) ?? [];

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{b.name}</h1>
          <p className="text-sm text-stone-500">
            {b.cooperageSupplier || "Unknown cooperage"}
            {b.charLevelSet && <> · char #{b.charLevel}</>}
            {b.capacityLSet && <> · {formatQty(b.capacityL)} L</>}
            {b.priorUse && <> · {b.priorUse}</>}
            {b.serialBurnin && <> · {b.serialBurnin}</>}
          </p>
          {(b.rickhouse || b.rowPosition || b.levelPosition || b.columnPosition) && (
            <p className="mt-1 text-xs text-stone-500">
              {[b.rickhouse, b.rowPosition && `row ${b.rowPosition}`, b.levelPosition && `lvl ${b.levelPosition}`, b.columnPosition && `col ${b.columnPosition}`]
                .filter(Boolean)
                .join(" · ")}
            </p>
          )}
        </div>
        <div className="text-right">
          {b.fillDate ? (
            <>
              <p className="text-xs uppercase text-stone-500">Aging</p>
              <p className="text-2xl font-semibold text-stone-900">{b.daysAged} days</p>
              {b.canadianWhiskyEligible ? (
                <p className="text-xs text-emerald-700">Canadian Whisky eligible</p>
              ) : b.smallWood ? (
                <p className="text-xs text-blue-700">{b.daysToCanadianWhiskyEligible} d to CW eligibility</p>
              ) : (
                <p className="text-xs text-amber-700">Aging in non-small-wood vessel</p>
              )}
              {b.fillDate && <p className="mt-1 text-xs text-stone-500">filled {b.fillDate}</p>}
            </>
          ) : b.daysAgedAtDumpSet ? (
            <>
              <p className="text-xs uppercase text-stone-500">Last filled for</p>
              <p className="text-2xl font-semibold text-stone-900">{b.daysAgedAtDump} days</p>
              <p className="text-xs text-stone-500">currently empty</p>
            </>
          ) : (
            <p className="text-sm text-stone-500">Empty / never filled</p>
          )}
        </div>
      </header>

      <section className="mb-8 grid grid-cols-3 gap-4">
        <Stat label="Volume" value={`${formatQty(b.currentVolumeL)} L`} />
        <Stat label="ABV" value={b.currentAbvPctSet ? `${b.currentAbvPct.toFixed(2)}%` : "—"} />
        <Stat label="LAA" value={`${formatLAA(b.currentLaa)} L`} highlight />
      </section>

      <section className="mb-8 grid grid-cols-1 gap-4 lg:grid-cols-3">
        {b.currentVolumeL === 0 ? (
          <FillCard
            barrelId={b.id}
            containers={nonBarrelContainers}
            onSubmit={(m) => fill.mutate(m)}
            submitting={fill.isPending}
            error={fill.error}
          />
        ) : (
          <>
            <RegaugeCard
              barrel={b}
              onSubmit={(m) => regauge.mutate(m)}
              submitting={regauge.isPending}
              error={regauge.error}
              lastResult={regauge.data}
            />
            <DumpCard
              barrel={b}
              containers={nonBarrelContainers}
              onSubmit={(m) => dump.mutate(m)}
              submitting={dump.isPending}
              error={dump.error}
            />
          </>
        )}
      </section>

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Event history</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Event</th>
              <th className="px-4 py-3 text-right">Vol (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
              <th className="px-4 py-3">Notes</th>
              <th className="px-4 py-3"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {detail.data.events.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-3 text-stone-500">No events yet.</td></tr>
            )}
            {detail.data.events.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3 text-stone-600">
                  {e.eventDate ? new Date(Number(e.eventDate.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 font-medium text-stone-900">{eventKindLabels[e.kind] ?? "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{e.volumeLSet ? formatQty(e.volumeL) : "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{e.abvPctSet ? `${e.abvPct.toFixed(2)}%` : "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{e.laaSet ? formatLAA(e.laa) : "—"}</td>
                <td className="px-4 py-3 text-stone-600">{e.notes}</td>
                <td className="px-4 py-3 text-right">
                  {e.kind !== BarrelEventKind.REGAUGE && (
                    <button
                      onClick={() => onVoidEvent(e.id, e.kind)}
                      disabled={voidEvent.isPending}
                      className="text-xs text-stone-500 hover:text-red-700 disabled:opacity-50"
                    >
                      Void
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function FillCard({
  barrelId,
  containers,
  onSubmit,
  submitting,
  error,
}: {
  barrelId: string;
  containers: { id: string; name: string; currentLaa: number }[];
  onSubmit: (m: ReturnType<typeof create<typeof FillBarrelRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
  const [srcID, setSrcID] = useState("");
  const [vol, setVol] = useState("");
  const [abv, setAbv] = useState("");
  const [notes, setNotes] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!srcID || !vol || !abv) return;
    onSubmit(
      create(FillBarrelRequestSchema, {
        barrelId,
        sourceContainerId: srcID,
        volumeL: Number(vol),
        abvPct: Number(abv),
        notes,
      }),
    );
  }
  return (
    <Card title="Fill barrel">
      <form onSubmit={submit} className="space-y-3 p-4">
        <Select label="From container" value={srcID} onChange={setSrcID}>
          <option value="">Select source…</option>
          {containers.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name} ({formatLAA(c.currentLaa)} L LAA on hand)
            </option>
          ))}
        </Select>
        <NumField label="Volume (L)" value={vol} onChange={setVol} />
        <NumField label="ABV %" value={abv} onChange={setAbv} />
        <TextField label="Notes" value={notes} onChange={setNotes} />
        <Submit submitting={submitting} error={error}>Fill</Submit>
      </form>
    </Card>
  );
}

function RegaugeCard({
  barrel,
  onSubmit,
  submitting,
  error,
  lastResult,
}: {
  barrel: { id: string; currentVolumeL: number; currentAbvPct: number; currentLaa: number };
  onSubmit: (m: ReturnType<typeof create<typeof RegaugeBarrelRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
  lastResult?: { lostLaa: number } | undefined;
}) {
  const [vol, setVol] = useState(String(barrel.currentVolumeL));
  const [abv, setAbv] = useState(String(barrel.currentAbvPct));
  const [notes, setNotes] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(
      create(RegaugeBarrelRequestSchema, {
        barrelId: barrel.id,
        newVolumeL: Number(vol),
        newAbvPct: Number(abv),
        notes,
      }),
    );
  }
  return (
    <Card title="Regauge (record current actual)">
      <form onSubmit={submit} className="space-y-3 p-4">
        <NumField label="Actual volume (L)" value={vol} onChange={setVol} />
        <NumField label="Actual ABV %" value={abv} onChange={setAbv} />
        <TextField label="Notes" value={notes} onChange={setNotes} />
        <Submit submitting={submitting} error={error}>Regauge</Submit>
        {lastResult && lastResult.lostLaa > 0 && (
          <p className="text-xs text-amber-700">Recorded loss: {formatLAA(lastResult.lostLaa)} L LAA</p>
        )}
        <p className="text-xs text-stone-500">
          Difference vs current is recorded as a loss_evaporation movement.
        </p>
      </form>
    </Card>
  );
}

function DumpCard({
  barrel,
  containers,
  onSubmit,
  submitting,
  error,
}: {
  barrel: { id: string; currentVolumeL: number; currentAbvPct: number };
  containers: { id: string; name: string }[];
  onSubmit: (m: ReturnType<typeof create<typeof DumpBarrelRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
  const [destID, setDestID] = useState("");
  const [vol, setVol] = useState(String(barrel.currentVolumeL));
  const [abv, setAbv] = useState(String(barrel.currentAbvPct));
  const [notes, setNotes] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!destID) return;
    onSubmit(
      create(DumpBarrelRequestSchema, {
        barrelId: barrel.id,
        destinationContainerId: destID,
        volumeL: Number(vol),
        abvPct: Number(abv),
        notes,
      }),
    );
  }
  return (
    <Card title="Dump barrel">
      <form onSubmit={submit} className="space-y-3 p-4">
        <Select label="Into container" value={destID} onChange={setDestID}>
          <option value="">Select destination…</option>
          {containers.map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </Select>
        <NumField label="Actual volume (L)" value={vol} onChange={setVol} />
        <NumField label="Actual ABV %" value={abv} onChange={setAbv} />
        <TextField label="Notes" value={notes} onChange={setNotes} />
        <Submit submitting={submitting} error={error}>Dump</Submit>
      </form>
    </Card>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
      <header className="border-b border-stone-200 bg-stone-50 px-4 py-3">
        <h2 className="text-sm font-semibold uppercase text-stone-500">{title}</h2>
      </header>
      {children}
    </div>
  );
}

function NumField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-stone-500">{label}</label>
      <input
        type="number"
        step="0.01"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
      />
    </div>
  );
}

function TextField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-stone-500">{label}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
      />
    </div>
  );
}

function Select({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-stone-500">{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
      >
        {children}
      </select>
    </div>
  );
}

function Submit({ submitting, error, children }: { submitting: boolean; error: Error | null; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3">
      <button
        type="submit"
        disabled={submitting}
        className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
      >
        {submitting ? "Saving…" : children}
      </button>
      {error && (
        <span className="text-xs text-red-600">
          {error instanceof ConnectError ? error.rawMessage : String(error)}
        </span>
      )}
    </div>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="rounded-lg border border-stone-200 bg-white p-4 shadow-sm">
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-emerald-700" : "text-stone-900"}`}>{value}</p>
    </div>
  );
}
