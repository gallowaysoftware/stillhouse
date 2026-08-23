import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { webhookClient } from "@/lib/clients";
import { WebhookEventKind } from "@/gen/stillhouse/v1/webhook_pb";
import { OwnerOnly } from "@/lib/role";

/**
 * WebhooksPanel — outbound notifications to another system.
 *
 * Two things this screen is careful about.
 *
 * The signing secret is shown exactly once, at creation, and never again.
 * A secret an API will read back is one that leaks through every log,
 * screenshot and support ticket that ever touches it, and this one signs
 * deliveries as us — anybody holding it can forge a message the receiver
 * will believe.
 *
 * And the delivery log is the point of the feature, not a debugging
 * extra. A webhook nobody can tell has stopped arriving is worse than no
 * webhook, because the receiver has no way to know it is missing data.
 */
const KINDS: [WebhookEventKind, string][] = [
  [WebhookEventKind.B266_PERIOD_SUBMITTED, "B266 period submitted"],
  [WebhookEventKind.BOTTLING_RUN_RECORDED, "Bottling run recorded"],
  [WebhookEventKind.REMOVAL_RECORDED, "Removal recorded"],
  [WebhookEventKind.PRODUCTION_GAUGE_RECORDED, "Production gauge recorded"],
  [WebhookEventKind.LOSS_RECORDED, "Loss recorded"],
];

function kindLabel(k: WebhookEventKind): string {
  return KINDS.find(([v]) => v === k)?.[1] ?? "—";
}

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function WebhooksPanel() {
  const qc = useQueryClient();
  const endpoints = useQuery({
    queryKey: ["webhookEndpoints"],
    queryFn: () => webhookClient.listWebhookEndpoints({}),
  });
  const deliveries = useQuery({
    queryKey: ["webhookDeliveries"],
    queryFn: () => webhookClient.listWebhookDeliveries({ limit: 25 }),
  });

  const [url, setUrl] = useState("");
  const [description, setDescription] = useState("");
  const [kinds, setKinds] = useState<Set<WebhookEventKind>>(new Set());
  const [secret, setSecret] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      webhookClient.createWebhookEndpoint({ url, description, kinds: [...kinds] }),
    onSuccess: (r) => {
      setSecret(r.secret);
      setUrl("");
      setDescription("");
      setKinds(new Set());
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["webhookEndpoints"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const setEnabled = useMutation({
    mutationFn: (v: { id: string; enabled: boolean }) =>
      webhookClient.setWebhookEndpointEnabled(v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["webhookEndpoints"] }),
  });
  const remove = useMutation({
    mutationFn: (id: string) => webhookClient.deleteWebhookEndpoint({ id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["webhookEndpoints"] }),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setSecret(null);
    create.mutate();
  }

  return (
    <section className="mb-8">
      <h2 className="mb-1 text-sm font-semibold text-fg-muted">Webhooks</h2>
      <p className="mb-3 text-sm text-fg-muted">
        Tell another system when something happens here. Deliveries are signed,
        retried, and logged below — a webhook nobody can tell has stopped
        arriving is worse than none.
      </p>

      {secret && (
        <Callout tone="success" title="Signing secret — shown once">
          <p className="break-all font-mono text-xs">{secret}</p>
          <p className="mt-2 text-xs">
            Copy it now. It is sealed at rest and will not be shown again — a
            secret an API reads back leaks through every log and screenshot that
            touches it. Verify a delivery with{" "}
            <code>HMAC-SHA256(secret, "&lt;unix&gt;." + body)</code> against the{" "}
            <code>X-Stillhouse-Signature</code> header; the timestamp is inside
            the MAC so a captured delivery cannot be replayed.
          </p>
        </Callout>
      )}

      <OwnerOnly>
        <form onSubmit={submit} className="mb-4 rounded-lg border border-border bg-surface-2 p-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-sm text-fg-muted">Endpoint URL</label>
              <input
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://example.com/stillhouse"
                className="w-full rounded border border-border-strong px-3 py-2 text-sm"
              />
              <p className="mt-1 text-xs text-fg-subtle">
                https only, and it must not resolve to a private or loopback
                address — checked here and again on every delivery.
              </p>
            </div>
            <div>
              <label className="mb-1 block text-sm text-fg-muted">Description</label>
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                className="w-full rounded border border-border-strong px-3 py-2 text-sm"
              />
            </div>
          </div>
          <div className="mt-3">
            <span className="mb-1 block text-sm text-fg-muted">Events</span>
            <div className="flex flex-wrap gap-2">
              {KINDS.map(([k, label]) => (
                <label key={k} className="flex items-center gap-1 text-sm">
                  <input
                    type="checkbox"
                    checked={kinds.has(k)}
                    onChange={(e) => {
                      const next = new Set(kinds);
                      e.target.checked ? next.add(k) : next.delete(k);
                      setKinds(next);
                    }}
                  />
                  {label}
                </label>
              ))}
            </div>
          </div>
          <button
            type="submit"
            disabled={create.isPending || !url || kinds.size === 0}
            className="mt-3 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {create.isPending ? "Adding…" : "Add endpoint"}
          </button>
          {err && <p className="mt-2 text-sm text-danger-fg">{err}</p>}
        </form>
      </OwnerOnly>

      <div className="mb-6 overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">URL</th>
              <th className="px-4 py-2">Events</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(endpoints.data?.endpoints ?? []).length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-fg-muted">No endpoints.</td></tr>
            )}
            {endpoints.data?.endpoints.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-2">
                  <div className="break-all">{e.url}</div>
                  {e.description && <div className="text-xs text-fg-subtle">{e.description}</div>}
                </td>
                <td className="px-4 py-2 text-xs text-fg-muted">
                  {e.kinds.map(kindLabel).join(", ")}
                </td>
                <td className="px-4 py-2">{e.enabled ? "enabled" : "disabled"}</td>
                <td className="px-4 py-2 text-right">
                  <OwnerOnly>
                    <button
                      onClick={() => setEnabled.mutate({ id: e.id, enabled: !e.enabled })}
                      className="mr-2 text-xs underline"
                    >
                      {e.enabled ? "disable" : "enable"}
                    </button>
                    <button onClick={() => remove.mutate(e.id)} className="text-xs text-danger-fg underline">
                      delete
                    </button>
                  </OwnerOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h3 className="mb-2 text-xs font-semibold uppercase text-fg-muted">Recent deliveries</h3>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">When</th>
              <th className="px-4 py-2">Event</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2">Attempts</th>
              <th className="px-4 py-2">Detail</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(deliveries.data?.deliveries ?? []).length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-fg-muted">Nothing delivered yet.</td></tr>
            )}
            {deliveries.data?.deliveries.map((d) => (
              <tr key={d.id}>
                <td className="px-4 py-2 tabular-nums text-fg-muted">{d.createdAt}</td>
                <td className="px-4 py-2">{kindLabel(d.kind)}</td>
                <td className={`px-4 py-2 ${d.status === "failed" ? "text-danger-fg" : ""}`}>
                  {d.status}
                  {d.status === "pending" && d.attempts > 0 && (
                    <span className="block text-xs text-fg-subtle">retry {d.nextAttemptAt}</span>
                  )}
                </td>
                <td className="px-4 py-2 tabular-nums">{d.attempts}</td>
                <td className="px-4 py-2 text-xs text-fg-muted">
                  {d.lastStatusCode ? `HTTP ${d.lastStatusCode}` : ""} {d.lastError}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
