import { FormEvent, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Button } from "@/components/Button";
import { Callout } from "@/components/Callout";
import { Shell } from "@/components/Shell";
import { pricingClient, productClient } from "@/lib/clients";
import {
  ChannelPricing,
  ComputeProvincialPricingRequestSchema,
  JurisdictionPricing,
  RateProvenance,
} from "@/gen/stillhouse/v1/pricing_pb";
import { formatCAD, formatLAA } from "@/lib/format";

/**
 * PricingPage — the same bottle through the three channels a distillery
 * actually sells into.
 *
 * The important thing this page does is refuse. Provincial markup rates
 * aren't in legislation; they're board policy, and several of the ones
 * that matter aren't published at all. Where a rate is missing the channel
 * says so and names where to get the number, rather than rendering a
 * confident figure built on a guess — which is what it used to do.
 */
export function PricingPage() {
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const [productId, setProductId] = useState("");
  const [fob, setFob] = useState("");
  const [shelf, setShelf] = useState("");

  const compute = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof ComputeProvincialPricingRequestSchema>>) =>
      pricingClient.computeProvincialPricing(msg),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    compute.mutate(
      create(ComputeProvincialPricingRequestSchema, {
        productId,
        fobCad: Number(fob) || 0,
        onSiteRetailPriceCad: Number(shelf) || 0,
      }),
    );
  }

  const data = compute.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Provincial pricing</h1>
        <p className="max-w-3xl text-sm text-fg-muted">
          One bottle through three channels: sale to the provincial board, your own shop, and
          export. They price completely differently, so a single number for "what it sells for"
          is right for none of them.
        </p>
      </div>

      <Callout tone="info">
        <span className="font-medium">Rates are board policy, not law.</span> Provincial liquor
        statutes delegate pricing to the boards, so markups change without a legislative
        amendment and several aren't published at all. Every figure below is labelled with how
        much it can be trusted, and a channel whose rate nobody has found says so instead of
        guessing.
      </Callout>

      <form
        onSubmit={submit}
        className="my-6 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
      >
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Product</label>
          <select
            value={productId}
            onChange={(e) => setProductId(e.target.value)}
            required
            className="rounded border border-border-strong px-3 py-2 text-sm"
          >
            <option value="">Select product…</option>
            {products.data?.products.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} · {p.bottleSizeMl} mL · {p.targetAbvPct}%
              </option>
            ))}
          </select>
        </div>
        <Num label="FOB per bottle" hint="what you charge the board" value={fob} onChange={setFob} />
        <Num
          label="Your shelf price"
          hint="optional — for the on-site channel"
          value={shelf}
          onChange={setShelf}
        />
        <Button type="submit" disabled={compute.isPending || !productId || !fob}>
          {compute.isPending ? "Computing…" : "Compute"}
        </Button>
        {compute.error && (
          <span className="text-sm text-danger-fg">
            {compute.error instanceof ConnectError ? compute.error.rawMessage : String(compute.error)}
          </span>
        )}
      </form>

      {data && (
        <>
          <p className="mb-4 text-sm text-fg-muted">
            <span className="font-medium text-fg">{data.productName}</span> · {data.bottleSizeMl} mL
            at {data.bottleAbvPct}% ={" "}
            <span className="tabular-nums">{formatLAA(data.laaPerBottle)} L</span> absolute alcohol
            per bottle · federal duty {formatCAD(data.federalDutyPerLaa)}/LAA
          </p>
          <div className="space-y-4">
            {data.jurisdictions.map((j) => (
              <JurisdictionCard key={j.code} j={j} />
            ))}
          </div>
        </>
      )}
    </Shell>
  );
}

function JurisdictionCard({ j }: { j: JurisdictionPricing }) {
  return (
    <section className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg">{j.name}</h2>
        {j.notes && <p className="mt-1 max-w-4xl text-xs text-fg-muted">{j.notes}</p>}
      </header>
      <div className="grid grid-cols-1 gap-px bg-border md:grid-cols-3">
        {[j.wholesale, j.onSiteRetail, j.export].map(
          (c, i) => c && <ChannelCard key={i} c={c} />,
        )}
      </div>
    </section>
  );
}

const CHANNEL_NAMES = ["—", "To the board", "Your own shop", "Export"];

function ChannelCard({ c }: { c: ChannelPricing }) {
  // Where the numbers came from. Collapsed by default — most of the time
  // you want the price, not its paperwork — but one click away, because
  // a price you can't trace is a price you can't quote.
  const [showSources, setShowSources] = useState(false);
  return (
    <div className="bg-surface-2 p-4">
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium text-fg-muted">{CHANNEL_NAMES[c.channel] ?? "—"}</span>
        <ProvenanceChip p={c.lowestProvenance} computable={c.computable} />
      </div>

      {c.computable ? (
        <>
          <p className="text-2xl font-bold text-fg">{formatCAD(c.distilleryNetCad)}</p>
          <p className="text-[11px] text-fg-muted">you keep, per bottle</p>
          <dl className="mt-3 space-y-1 text-xs tabular-nums">
            {c.priceToBuyerCad > 0 && <Line k="Buyer pays" v={formatCAD(c.priceToBuyerCad)} />}
            {c.landedCostCad > 0 && <Line k="Landed cost" v={formatCAD(c.landedCostCad)} />}
            {c.markupCad > 0 && <Line k="Mark-up / remittance" v={formatCAD(c.markupCad)} />}
            {c.federalExciseCad > 0 && <Line k="Federal excise" v={formatCAD(c.federalExciseCad)} />}
            {c.provincialTaxCad > 0 && <Line k="Provincial tax" v={formatCAD(c.provincialTaxCad)} />}
            {c.cosdCad > 0 && <Line k="Cost of service" v={formatCAD(c.cosdCad)} />}
            {c.salesTaxCad > 0 && <Line k="Sales tax" v={formatCAD(c.salesTaxCad)} />}
            {c.containerDepositCad > 0 && <Line k="Deposit" v={formatCAD(c.containerDepositCad)} />}
          </dl>
        </>
      ) : (
        <p className="text-sm font-medium text-fg-muted">Can't be priced yet</p>
      )}

      {c.computable && c.citations.length > 0 && (
        <div className="mt-3 border-t border-border pt-2">
          <button
            onClick={() => setShowSources((v) => !v)}
            className="text-[11px] text-fg-muted hover:text-fg"
          >
            {showSources ? "Hide" : "Where these numbers came from"} ({c.citations.length})
          </button>
          {showSources && (
            <ul className="mt-2 space-y-1.5">
              {c.citations.map((cit, i) => (
                <li key={i} className="text-[11px] leading-snug">
                  <span className="text-fg">{cit.what}</span>
                  <span className="ml-1 text-fg-muted">
                    {cit.provenance === RateProvenance.UNKNOWN
                      ? "— not found"
                      : `— ${cit.value}`}
                  </span>
                  <span className="ml-1 text-fg-subtle">
                    ({provenanceWord(cit.provenance)})
                  </span>
                  {cit.source && (
                    <span className="block text-fg-subtle">
                      {cit.source}
                      {cit.asOf && cit.asOf !== "unknown" && <> · as of {cit.asOf}</>}
                    </span>
                  )}
                  {cit.note && <span className="block text-fg-subtle">{cit.note}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {c.missing.length > 0 && (
        <ul className="mt-3 space-y-2">
          {c.missing.map((m, i) => (
            <li
              key={i}
              className={`rounded border border-l-4 px-2 py-1.5 text-[11px] ${
                c.computable
                  ? "border-info/40 border-l-info bg-info/10"
                  : "border-warning/40 border-l-warning bg-warning/10"
              }`}
            >
              <p className={`font-medium ${c.computable ? "text-info-fg" : "text-warning-fg"}`}>
                {m.what}
              </p>
              <p className="mt-0.5 text-fg-muted">{m.why}</p>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * ProvenanceChip is the whole point of the rewrite: a number's worth is
 * the worth of the weakest rate inside it, and that should be visible
 * without reading the code.
 */
// The one-word form, for a citation line. The chip above is the
// figure-level summary; this labels an individual rate.
function provenanceWord(p: RateProvenance): string {
  switch (p) {
    case RateProvenance.SOURCED: return "published";
    case RateProvenance.INDICATIVE: return "indicative — planning only";
    case RateProvenance.UNKNOWN: return "not found";
    default: return "unspecified";
  }
}

function ProvenanceChip({ p, computable }: { p: RateProvenance; computable: boolean }) {
  if (!computable) {
    return (
      <span className="rounded bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning-fg">
        rate unknown
      </span>
    );
  }
  switch (p) {
    case RateProvenance.SOURCED:
      return (
        <span
          className="rounded bg-success/15 px-1.5 py-0.5 text-[10px] font-medium text-success-fg"
          title="Every rate used came from the board's or the legislature's own published material."
        >
          sourced
        </span>
      );
    case RateProvenance.INDICATIVE:
      return (
        <span
          className="rounded bg-info/15 px-1.5 py-0.5 text-[10px] font-medium text-info-fg"
          title="Leans on at least one rate from a secondary source. Fine for planning; don't quote a customer from it."
        >
          indicative
        </span>
      );
    default:
      return (
        <span
          className="rounded bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning-fg"
          title="An amount the province may take is unrecorded, so this is an upper bound."
        >
          upper bound
        </span>
      );
  }
}

function Line({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <dt className="text-fg-muted">{k}</dt>
      <dd className="text-fg">{v}</dd>
    </div>
  );
}

function Num({
  label,
  hint,
  value,
  onChange,
}: {
  label: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <label className="mb-2 block text-sm font-medium text-fg-muted">{label}</label>
      <input
        type="number"
        step="0.01"
        min="0"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-36 rounded border border-border-strong px-3 py-2 text-sm tabular-nums"
      />
      <p className="mt-1 text-[11px] text-fg-subtle">{hint}</p>
    </div>
  );
}
