import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import { auditClient } from "@/lib/clients";
import { AuditAction } from "@/gen/stillhouse/v1/audit_pb";

const actionLabels: Record<AuditAction, string> = {
  [AuditAction.UNSPECIFIED]: "—",
  [AuditAction.CREATE]: "Create",
  [AuditAction.UPDATE]: "Update",
  [AuditAction.DELETE]: "Delete",
  [AuditAction.SIGN]: "Sign",
  [AuditAction.LOGIN]: "Login",
  [AuditAction.LOGOUT]: "Logout",
};

function buildExportUrl(entityType: string, from: string, to: string): string {
  const params = new URLSearchParams();
  if (entityType) params.set("entity_type", entityType);
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  const qs = params.toString();
  return qs ? `/export/audit.csv?${qs}` : "/export/audit.csv";
}

export function AuditPage() {
  const [entityType, setEntityType] = useState("");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const { data, isLoading } = useQuery({
    queryKey: ["listAuditEvents", entityType],
    queryFn: () =>
      auditClient.listAuditEvents({
        entityType,
        limit: 200,
      }),
  });

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Audit log</h1>
        <p className="text-sm text-fg-muted">
          Append-only journal of significant actions. Backs s.206 records-keeping
          obligations under the Excise Act, 2001 (6-year retention).
        </p>
      </div>

      <div className="mb-4 flex items-end gap-3">
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Filter by entity type</label>
          <input
            value={entityType}
            onChange={(e) => setEntityType(e.target.value)}
            placeholder="(any)"
            className="w-64 rounded border border-border-strong px-3 py-2 text-sm"
          />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">From (export only)</label>
          <input type="date" value={fromDate} onChange={(e) => setFromDate(e.target.value)} className="rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">To (export only)</label>
          <input type="date" value={toDate} onChange={(e) => setToDate(e.target.value)} className="rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <a
          href={buildExportUrl(entityType, fromDate, toDate)}
          className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3"
        >
          Export CSV
        </a>
        <p className="text-xs text-fg-muted">
          {data && <>{data.totalCount.toString()} total events</>}
        </p>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Actor</th>
              <th className="px-4 py-3">Action</th>
              <th className="px-4 py-3">Entity</th>
              <th className="px-4 py-3">Payload</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading && (
              <tr><td colSpan={5} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!isLoading && data?.events.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-fg-muted">No events.</td></tr>
            )}
            {data?.events.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3 text-fg-muted">
                  {e.occurredAt ? new Date(Number(e.occurredAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 text-fg-muted">
                  {e.userEmail ? (
                    <>
                      <div className="text-fg">{e.userDisplayName || e.userEmail}</div>
                      <div className="text-xs text-fg-muted">{e.userEmail}</div>
                    </>
                  ) : (
                    <span className="text-xs italic text-fg-subtle">system</span>
                  )}
                </td>
                <td className="px-4 py-3 text-fg">{actionLabels[e.action]}</td>
                <td className="px-4 py-3 text-fg-muted">
                  <div className="font-mono text-xs">{e.entityType}</div>
                  <div className="font-mono text-xs text-fg-subtle">{e.entityId.slice(0, 8)}…</div>
                </td>
                <td className="px-4 py-3 text-xs">
                  <code className="break-all text-fg">{decodePayload(e.payloadJson)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function decodePayload(p: Uint8Array): string {
  if (!p || p.length === 0) return "";
  try {
    return new TextDecoder().decode(p);
  } catch {
    return "(binary)";
  }
}
