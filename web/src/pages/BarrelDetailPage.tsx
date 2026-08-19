import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Button } from "@/components/Button";
import { Callout } from "@/components/Callout";
import { useConfirm } from "@/components/ConfirmDialog";
import { MaturationPanel } from "@/components/MaturationPanel";
import { Shell } from "@/components/Shell";
import {
  emptyReading,
  readingToRequest,
  SourceBadge,
  StrengthReading,
  StrengthReadingValue,
} from "@/components/StrengthReading";
import { useToast } from "@/components/Toast";
import { barrelClient, bulkClient } from "@/lib/clients";
import {
  BarrelEventKind,
  DumpBarrelRequestSchema,
  FillBarrelRequestSchema,
  RegaugeBarrelRequestSchema,
  VoidBarrelEventRequestSchema,
} from "@/gen/stillhouse/v1/barrel_pb";
import { StrengthSource } from "@/gen/stillhouse/v1/alcoholometry_pb";
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
  const confirm = useConfirm();
  const toast = useToast();
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
    onSuccess: () => { refresh(); toast("success", "Barrel filled."); },
  });
  const regauge = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RegaugeBarrelRequestSchema>>) =>
      barrelClient.regaugeBarrel(msg),
    onSuccess: () => { refresh(); toast("success", "Regauge recorded."); },
  });
  const dump = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DumpBarrelRequestSchema>>) =>
      barrelClient.dumpBarrel(msg),
    onSuccess: () => { refresh(); toast("success", "Barrel dumped."); },
  });
  const voidEvent = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidBarrelEventRequestSchema>>) =>
      barrelClient.voidBarrelEvent(msg),
    onSuccess: () => { refresh(); toast("success", "Event voided."); },
  });

  async function onVoidEvent(eventId: string, kind: BarrelEventKind) {
    const ok = await confirm({
      title: `Void ${eventKindLabels[kind]} event?`,
      body: <>This reverses the linked bulk movement and recomputes both containers' balances. The original event row stays for audit.</>,
      consequences: [
        "Source / destination tank balances adjust by the inverse of this event",
        "An offsetting regauge_correction movement gets written to the ledger",
        "Regauge events can't be voided — record a corrective regauge instead",
      ],
      requireReason: { label: "Reason", placeholder: "recorded in error" },
      confirmLabel: "Void event",
      tone: "danger",
    });
    if (!ok) return;
    voidEvent.mutate(create(VoidBarrelEventRequestSchema, { id: eventId, reason: ok.reason }));
  }

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (detail.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!detail.data?.barrel) return <Shell><p>Not found.</p></Shell>;
  const b = detail.data.barrel;

  const nonBarrelContainers =
    containers.data?.containers.filter((c) => !c.archived && c.id !== b.id) ?? [];

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">{b.name}</h1>
          <p className="text-sm text-fg-muted">
            {b.cooperageSupplier || "Unknown cooperage"}
            {b.charLevelSet && <> · char #{b.charLevel}</>}
            {b.capacityLSet && <> · {formatQty(b.capacityL)} L</>}
            {b.priorUse && <> · {b.priorUse}</>}
            {b.serialBurnin && <> · {b.serialBurnin}</>}
          </p>
          {(b.rickhouse || b.rowPosition || b.levelPosition || b.columnPosition) && (
            <p className="mt-1 text-xs text-fg-muted">
              {[b.rickhouse, b.rowPosition && `row ${b.rowPosition}`, b.levelPosition && `lvl ${b.levelPosition}`, b.columnPosition && `col ${b.columnPosition}`]
                .filter(Boolean)
                .join(" · ")}
            </p>
          )}
        </div>
        <div className="text-right">
          {b.fillDate ? (
            <>
              <p className="text-xs text-fg-muted">Aging</p>
              <p className="text-3xl font-bold tracking-tight text-fg">{b.daysAged} days</p>
              {b.canadianWhiskyEligible ? (
                <p className="text-xs text-success-fg">Canadian Whisky eligible</p>
              ) : b.smallWood ? (
                <p className="text-xs text-info-fg">{b.daysToCanadianWhiskyEligible} d to CW eligibility</p>
              ) : (
                <p className="text-xs text-warning-fg">Aging in non-small-wood vessel</p>
              )}
              {b.fillDate && <p className="mt-1 text-xs text-fg-muted">filled {b.fillDate}</p>}
            </>
          ) : b.daysAgedAtDumpSet ? (
            <>
              <p className="text-xs text-fg-muted">Last filled for</p>
              <p className="text-3xl font-bold tracking-tight text-fg">{b.daysAgedAtDump} days</p>
              <p className="text-xs text-fg-muted">currently empty</p>
            </>
          ) : (
            <p className="text-sm text-fg-muted">Empty / never filled</p>
          )}
        </div>
      </header>

      <section className="mb-8 grid grid-cols-3 gap-4">
        <Stat label="Volume" value={`${formatQty(b.currentVolumeL)} L`} />
        <Stat label="ABV" value={b.currentAbvPctSet ? `${b.currentAbvPct.toFixed(2)}%` : "—"} />
        <Stat label="LAA" value={`${formatLAA(b.currentLaa)} L`} highlight />
      </section>

      {detail.data.maturation && (
        <section className="mb-8 max-w-xl">
          <MaturationPanel m={detail.data.maturation} />
        </section>
      )}

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

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Event history</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Event</th>
              <th className="px-4 py-3 text-right">Vol (L @ 20 °C)</th>
              <th className="px-4 py-3 text-right">Strength (20 °C)</th>
              <th className="px-4 py-3 text-right">LAA</th>
              <th className="px-4 py-3">Notes</th>
              <th className="px-4 py-3"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {detail.data.events.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-3 text-fg-muted">No events yet.</td></tr>
            )}
            {detail.data.events.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3 text-fg-muted">
                  {e.eventDate ? new Date(Number(e.eventDate.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3">
                  <div className="font-medium text-fg">{eventKindLabels[e.kind] ?? "—"}</div>
                  {/* Only worth the ink when a determination was actually
                      made — an unbadged row is an uncorrected legacy one. */}
                  {e.strengthSource !== StrengthSource.UNCORRECTED && (
                    <div className="mt-1"><SourceBadge source={e.strengthSource} /></div>
                  )}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted tabular-nums">
                  {e.volumeLSet ? formatQty(e.volumeL) : "—"}
                  {Math.abs(e.volumeFactorC - 1) > 1e-9 && e.observedVolumeL > 0 && (
                    <div className="text-xs text-fg-subtle">
                      gauged {formatQty(e.observedVolumeL)}
                      {e.temperatureCSet && ` @ ${e.temperatureC.toFixed(1)}°C`}
                    </div>
                  )}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted tabular-nums">
                  {e.abvPctSet ? `${e.abvPct.toFixed(2)}%` : "—"}
                  {e.observedDensityKgM3Set && (
                    <div className="text-xs text-fg-subtle">{e.observedDensityKgM3.toFixed(1)} kg/m³</div>
                  )}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted tabular-nums">{e.laaSet ? formatLAA(e.laa) : "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{e.notes}</td>
                <td className="px-4 py-3 text-right">
                  {e.kind !== BarrelEventKind.REGAUGE && (
                    <button
                      onClick={() => onVoidEvent(e.id, e.kind)}
                      disabled={voidEvent.isPending}
                      className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
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
  containers: { id: string; name: string; currentLaa: number; currentAbvPct: number; currentAbvPctSet: boolean }[];
  onSubmit: (m: ReturnType<typeof create<typeof FillBarrelRequestSchema>>) => void;
  submitting: boolean;
  error: Error | null;
}) {
  const [srcID, setSrcID] = useState("");
  const [reading, setReading] = useState<StrengthReadingValue>(emptyReading());
  const [notes, setNotes] = useState("");
  const selectedSource = containers.find((c) => c.id === srcID);
  const sourceHasNoABV = selectedSource && !selectedSource.currentAbvPctSet;
  // Prefill strength from the source the first time it's selected —
  // operators almost always fill at the container's current strength,
  // which is already held at 20 °C. Saves re-typing.
  function onSourceChange(id: string) {
    setSrcID(id);
    const next = containers.find((c) => c.id === id);
    if (next && next.currentAbvPctSet && !reading.abv && reading.mode === "abv") {
      setReading((r) => ({ ...r, abv: next.currentAbvPct.toFixed(2) }));
    }
  }
  const hasStrength = reading.mode === "density" ? reading.density.trim() !== "" : reading.abv.trim() !== "";
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!srcID || !reading.volumeL || !hasStrength) return;
    onSubmit(
      create(FillBarrelRequestSchema, {
        barrelId,
        sourceContainerId: srcID,
        volumeL: Number(reading.volumeL),
        notes,
        ...readingToRequest(reading),
      }),
    );
  }
  return (
    <Card title="Fill barrel">
      <form onSubmit={submit} className="space-y-3 p-4">
        <Select label="From container" value={srcID} onChange={onSourceChange}>
          <option value="">Select source…</option>
          {containers.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name} ({formatLAA(c.currentLaa)} L LAA on hand)
              {c.currentAbvPctSet ? "" : " — ABV NOT SET"}
            </option>
          ))}
        </Select>
        {sourceHasNoABV && (
          <Callout tone="danger">
            Source tank has no recorded ABV. Gauge it before filling — otherwise the LAA in the
            barrel (and in B266 downstream) will be wrong.
          </Callout>
        )}
        <StrengthReading value={reading} onChange={setReading} volumeLabel="Volume (L, as gauged)" />
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
  const [reading, setReading] = useState<StrengthReadingValue>(
    emptyReading({ volumeL: String(barrel.currentVolumeL), abv: String(barrel.currentAbvPct) }),
  );
  const [notes, setNotes] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(
      create(RegaugeBarrelRequestSchema, {
        barrelId: barrel.id,
        newVolumeL: Number(reading.volumeL),
        newAbvPct: reading.mode === "abv" ? Number(reading.abv) : 0,
        notes,
        ...readingToRequest(reading),
      }),
    );
  }
  return (
    <Card title="Regauge (record current actual)">
      <form onSubmit={submit} className="space-y-3 p-4">
        <StrengthReading value={reading} onChange={setReading} volumeLabel="Actual volume (L, as gauged)" />
        <TextField label="Notes" value={notes} onChange={setNotes} />
        <Submit submitting={submitting} error={error}>Regauge</Submit>
        {lastResult && lastResult.lostLaa > 0 && (
          <p className="text-xs text-warning-fg">Recorded loss: {formatLAA(lastResult.lostLaa)} L LAA</p>
        )}
        <p className="text-xs text-fg-muted">
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
  const [reading, setReading] = useState<StrengthReadingValue>(
    emptyReading({ volumeL: String(barrel.currentVolumeL), abv: String(barrel.currentAbvPct) }),
  );
  const [notes, setNotes] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!destID) return;
    onSubmit(
      create(DumpBarrelRequestSchema, {
        barrelId: barrel.id,
        destinationContainerId: destID,
        volumeL: Number(reading.volumeL),
        notes,
        ...readingToRequest(reading),
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
        <StrengthReading value={reading} onChange={setReading} volumeLabel="Actual volume (L, as gauged)" />
        <TextField label="Notes" value={notes} onChange={setNotes} />
        <Submit submitting={submitting} error={error}>Dump</Submit>
      </form>
    </Card>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">{title}</h2>
      </header>
      {children}
    </div>
  );
}


function TextField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <div>
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
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-border-strong px-3 py-2 text-sm"
      >
        {children}
      </select>
    </div>
  );
}

function Submit({ submitting, error, children }: { submitting: boolean; error: Error | null; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3">
      <Button type="submit" disabled={submitting}>
        {submitting ? "Saving…" : children}
      </Button>
      {error && (
        <span className="text-xs text-danger-fg">
          {error instanceof ConnectError ? error.rawMessage : String(error)}
        </span>
      )}
    </div>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="rounded-lg border border-border bg-surface-2 p-4 shadow-sm">
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-success-fg" : "text-fg"}`}>{value}</p>
    </div>
  );
}
