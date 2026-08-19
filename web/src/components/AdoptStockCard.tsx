import { FormEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Button } from "@/components/Button";
import { Callout } from "@/components/Callout";
import { bulkClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";

/**
 * AdoptStockCard — day one.
 *
 * A working distillery adopting Stillhouse has casks in the warehouse with
 * no mash, no fermentation and no distillation run behind them. That
 * history lives in whatever they kept records in before and there is no
 * honest way to reconstruct it. What they do have is a scale and a
 * hydrometer, which is exactly what CRA's Mass/Density Procedure takes:
 * kilograms × A gives litres at 20 °C, and the hydrometer indication gives
 * the strength.
 *
 * Weight is the default entry mode because it's the better measurement —
 * a scale doesn't care about temperature and mass doesn't contract — and
 * because it's what a warehouse full of full casks can actually produce
 * without dipping every one.
 */
export function AdoptStockCard({
  containerId,
  containerName,
  isBarrel,
  onAdopted,
}: {
  containerId: string;
  containerName: string;
  isBarrel: boolean;
  onAdopted?: () => void;
}) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<"mass" | "volume">("mass");
  const [amount, setAmount] = useState("");
  const [density, setDensity] = useState("");
  const [temp, setTemp] = useState("20");
  const [fillDate, setFillDate] = useState("");
  const [asOf, setAsOf] = useState("");
  const [notes, setNotes] = useState("");

  const adopt = useMutation({
    mutationFn: () =>
      bulkClient.adoptOpeningInventory({
        containerId,
        massKg: mode === "mass" ? Number(amount) || 0 : 0,
        massKgSet: mode === "mass",
        volumeL: mode === "volume" ? Number(amount) || 0 : 0,
        volumeLSet: mode === "volume",
        densityKgM3: Number(density) || 0,
        densityKgM3Set: density.trim() !== "",
        temperatureC: Number(temp) || 0,
        temperatureCSet: temp.trim() !== "",
        fillDate,
        notes,
        ...(asOf ? { asOf: { seconds: BigInt(Math.floor(new Date(asOf).getTime() / 1000)), nanos: 0 } } : {}),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["getBarrel"] });
      qc.invalidateQueries({ queryKey: ["listBarrels"] });
      qc.invalidateQueries({ queryKey: ["getBulkContainer"] });
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      onAdopted?.();
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!amount.trim()) return;
    adopt.mutate();
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex items-center justify-between border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Adopt existing stock</h2>
        <div className="flex overflow-hidden rounded border border-border-strong text-[11px]">
          {(
            [
              ["mass", "Weighed"],
              ["volume", "Dipped"],
            ] as const
          ).map(([m, label]) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={`px-2 py-0.5 transition-colors ${
                mode === m
                  ? "bg-accent text-accent-fg font-medium"
                  : "text-fg-muted hover:bg-surface-3 hover:text-fg"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </header>

      <form onSubmit={submit} className="space-y-3 p-4">
        <p className="text-xs text-fg-muted">
          Spirit already in {containerName} before Stillhouse. Goes into the ledger as opening
          inventory — not production — so it lands in your B266 opening balance rather than
          overstating what you made.
        </p>

        <div className="grid grid-cols-3 gap-3">
          <Num
            label={mode === "mass" ? "Weight" : "Volume"}
            suffix={mode === "mass" ? "kg" : "L"}
            value={amount}
            onChange={setAmount}
          />
          <Num label="Hydrometer" suffix="kg/m³" step="0.1" value={density} onChange={setDensity} />
          <Num label="Temp" suffix="°C" step="0.1" value={temp} onChange={setTemp} />
        </div>

        {isBarrel && (
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Originally filled</label>
            <input
              type="date"
              value={fillDate}
              onChange={(e) => setFillDate(e.target.value)}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
            <p className="mt-1 text-[11px] text-fg-subtle">
              The real fill date, so the cask keeps the age it has. Leave it blank and a
              three-year-old barrel restarts at zero and loses its Canadian Whisky eligibility.
            </p>
          </div>
        )}

        <div>
          <label className="mb-1 block text-xs text-fg-muted">On hand as of (optional)</label>
          <input
            type="date"
            value={asOf}
            onChange={(e) => setAsOf(e.target.value)}
            className="w-full rounded border border-border-strong px-3 py-2 text-sm"
          />
          <p className="mt-1 text-[11px] text-fg-subtle">
            Defaults to today. Back-date to the start of your first reporting period.
          </p>
        </div>

        <div>
          <label className="mb-1 block text-xs text-fg-muted">Notes</label>
          <input
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="prior records ref, cask history…"
            className="w-full rounded border border-border-strong px-3 py-2 text-sm"
          />
        </div>

        <Button type="submit" disabled={adopt.isPending || !amount.trim()}>
          {adopt.isPending ? "Adopting…" : "Adopt stock"}
        </Button>

        {adopt.error && (
          <Callout tone="danger">
            {adopt.error instanceof ConnectError ? adopt.error.rawMessage : String(adopt.error)}
          </Callout>
        )}

        {adopt.data && (
          <Callout tone="success">
            Adopted {formatQty(adopt.data.volumeL20c)} L at{" "}
            {adopt.data.strengthPct20c.toFixed(2)} % ={" "}
            <strong>{formatLAA(adopt.data.laa)} L LAA</strong>, all at 20 °C.
          </Callout>
        )}
      </form>
    </div>
  );
}

function Num({
  label,
  value,
  onChange,
  suffix,
  step = "0.01",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  suffix?: string;
  step?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <div className="relative">
        <input
          type="number"
          step={step}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`w-full rounded border border-border-strong px-3 py-2 text-sm tabular-nums ${
            suffix ? "pr-12" : ""
          }`}
        />
        {suffix && (
          <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-xs text-fg-subtle">
            {suffix}
          </span>
        )}
      </div>
    </div>
  );
}
