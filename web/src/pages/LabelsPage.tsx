import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Barcode } from "@/components/Barcode";
import { Shell } from "@/components/Shell";
import { labelClient } from "@/lib/clients";
import { LabelKind } from "@/gen/stillhouse/v1/label_pb";
import { formatLAA, formatQty } from "@/lib/format";

const kinds: { k: LabelKind; label: string; blurb: string }[] = [
  {
    k: LabelKind.BARREL,
    label: "Cask tags",
    blurb: "One per cask, for the bung stave or the head. Carries the fill, the age and the code.",
  },
  {
    k: LabelKind.CONTAINER,
    label: "Vessel labels",
    blurb: "Tanks, IBCs and totes.",
  },
  {
    k: LabelKind.LOT,
    label: "Case labels",
    blurb: "One per packaged lot, for the case or the pallet card. Carries the jurisdiction the stamps are for.",
  },
  {
    k: LabelKind.PRODUCT,
    label: "Product cards",
    blurb: "One per product, with the GTIN where the SKU registry has one.",
  },
];

export function LabelsPage() {
  const [kind, setKind] = useState<LabelKind>(LabelKind.BARREL);
  const [filter, setFilter] = useState("");
  const targets = useQuery({
    queryKey: ["listLabelTargets", kind],
    queryFn: () => labelClient.listLabelTargets({ kind }),
  });

  const chosen = kinds.find((x) => x.k === kind)!;
  const rows = (targets.data?.targets ?? []).filter((t) =>
    filter.trim() === ""
      ? true
      : `${t.title} ${t.subtitle} ${t.code}`.toLowerCase().includes(filter.toLowerCase()),
  );

  return (
    <Shell>
      <div data-print-hide className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Labels</h1>
        <p className="text-sm text-fg-muted">
          Finding cask 0417 by scrolling a list is not how a rackhouse is run.
          Print these, stick them on, and scan them — the code under each barcode
          is fourteen characters, so it can also be read out or typed in.
        </p>
      </div>

      <div data-print-hide className="mb-4 flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-fg-muted">What to print</label>
          <select
            value={kind}
            onChange={(e) => setKind(Number(e.target.value) as LabelKind)}
            className="rounded border border-border-strong px-2 py-1.5 text-sm"
          >
            {kinds.map((x) => (
              <option key={x.k} value={x.k}>{x.label}</option>
            ))}
          </select>
        </div>
        <div className="grow">
          <label className="mb-1 block text-xs text-fg-muted">Narrow it down</label>
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="name, code, cooperage…"
            className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
          />
        </div>
        <button
          onClick={() => window.print()}
          disabled={rows.length === 0}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          Print {rows.length} label{rows.length === 1 ? "" : "s"}
        </button>
      </div>

      <p data-print-hide className="mb-4 text-xs text-fg-subtle">{chosen.blurb}</p>

      {targets.isLoading && <p className="text-sm text-fg-muted">Loading…</p>}
      {!targets.isLoading && rows.length === 0 && (
        <p data-print-hide className="text-sm text-fg-muted">Nothing to print here yet.</p>
      )}

      {/* Two columns on paper, whatever the screen is doing. Sheet stock
          for thermal and laser both come two-up at this size, and a label
          that spans a page break is a wasted sheet. */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {rows.map((t) => (
          <div
            key={t.id}
            className="break-inside-avoid rounded border border-border bg-surface-2 p-3"
          >
            <div className="flex items-baseline justify-between gap-2">
              <span className="truncate text-sm font-semibold text-fg">{t.title}</span>
              {t.jurisdiction && (
                <span className="shrink-0 text-xs text-fg-muted">{t.jurisdiction}</span>
              )}
            </div>
            <div className="truncate text-xs text-fg-muted">{t.subtitle}</div>
            <div className="mt-1 text-xs text-fg-muted">
              {t.volumeL > 0 && <>{formatQty(t.volumeL)} L</>}
              {t.abvPct > 0 && <> · {t.abvPct.toFixed(1)} %</>}
              {t.laa > 0 && <> · {formatLAA(t.laa)} LAA</>}
              {t.bottles > 0 && <>{t.bottles} bottles</>}
              {t.fillDate && <> · filled {t.fillDate} ({t.daysAged} d)</>}
            </div>
            <div className="mt-2">
              <Barcode value={t.code} height={40} />
            </div>
            <div className="mt-0.5 text-center font-mono text-[11px] tracking-widest text-fg">
              {t.code}
            </div>
            {t.gtin && (
              <div className="mt-2">
                <Barcode value={t.gtin} height={28} />
                <div className="mt-0.5 text-center font-mono text-[10px] tracking-widest text-fg-muted">
                  GTIN {t.gtin}
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </Shell>
  );
}
