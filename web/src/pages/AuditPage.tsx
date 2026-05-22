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

export function AuditPage() {
  const [entityType, setEntityType] = useState("");
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
        <h1 className="text-2xl font-semibold">Audit log</h1>
        <p className="text-sm text-stone-500">
          Append-only journal of significant actions. Backs s.206 records-keeping
          obligations under the Excise Act, 2001 (6-year retention).
        </p>
      </div>

      <div className="mb-4 flex items-end gap-3">
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Filter by entity type</label>
          <input
            value={entityType}
            onChange={(e) => setEntityType(e.target.value)}
            placeholder="(any)"
            className="w-64 rounded border border-stone-300 px-3 py-2 text-sm"
          />
        </div>
        <a
          href={`/export/audit.csv${entityType ? `?entity_type=${encodeURIComponent(entityType)}` : ""}`}
          className="rounded border border-stone-300 px-3 py-2 text-sm text-stone-700 hover:bg-stone-100"
        >
          Export CSV
        </a>
        <p className="text-xs text-stone-500">
          {data && <>{data.totalCount.toString()} total events</>}
        </p>
      </div>

      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Actor</th>
              <th className="px-4 py-3">Action</th>
              <th className="px-4 py-3">Entity</th>
              <th className="px-4 py-3">Payload</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {isLoading && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!isLoading && data?.events.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">No events.</td></tr>
            )}
            {data?.events.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3 text-stone-600">
                  {e.occurredAt ? new Date(Number(e.occurredAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 text-stone-600">
                  {e.userEmail ? (
                    <>
                      <div className="text-stone-900">{e.userDisplayName || e.userEmail}</div>
                      <div className="text-xs text-stone-500">{e.userEmail}</div>
                    </>
                  ) : (
                    <span className="text-xs italic text-stone-400">system</span>
                  )}
                </td>
                <td className="px-4 py-3 text-stone-900">{actionLabels[e.action]}</td>
                <td className="px-4 py-3 text-stone-600">
                  <div className="font-mono text-xs">{e.entityType}</div>
                  <div className="font-mono text-xs text-stone-400">{e.entityId.slice(0, 8)}…</div>
                </td>
                <td className="px-4 py-3 text-xs">
                  <code className="break-all text-stone-700">{decodePayload(e.payloadJson)}</code>
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
