import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { alertClient } from "@/lib/clients";
import { Alert, AlertKind, AlertSeverity } from "@/gen/stillhouse/v1/alert_pb";

/**
 * The alerts the server raised, rendered into the dashboard's existing
 * "Alerts" section rather than as a second one beside it.
 *
 * The distinction from the callouts that section already had: those are
 * computed in the browser from lists the page happened to fetch, and
 * exist only while you are looking at them. These persist, have a life
 * cycle, and get emailed. Conditions that moved to the server — a return
 * due, stamps running low — were removed from the client-side set in the
 * same change, because the same fact appearing twice is worse than
 * either version alone.
 *
 * There is no dismiss button, on purpose: an alert is a condition, so it
 * goes away when the condition does and not before. "Seen" acknowledges
 * it — a statement about the reader's attention, not about the world.
 */
export function ServerAlerts() {
  const qc = useQueryClient();
  const alerts = useQuery({
    queryKey: ["listAlerts"],
    queryFn: () => alertClient.listAlerts({}),
    refetchInterval: 5 * 60 * 1000,
  });
  const acknowledge = useMutation({
    mutationFn: (id: string) => alertClient.acknowledgeAlert({ id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listAlerts"] }),
  });
  const reevaluate = useMutation({
    mutationFn: () => alertClient.evaluateAlerts({}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listAlerts"] }),
  });

  const open = alerts.data?.alerts ?? [];
  if (alerts.isPending || open.length === 0) return null;

  return (
    <>
      <ul className="space-y-2">
        {open.map((a) => (
          <AlertRow
            key={a.id}
            alert={a}
            onAcknowledge={() => acknowledge.mutate(a.id)}
            acknowledging={acknowledge.isPending}
          />
        ))}
      </ul>
      <button
        onClick={() => reevaluate.mutate()}
        disabled={reevaluate.isPending}
        className="mt-2 text-xs text-fg-muted hover:text-fg disabled:opacity-50"
      >
        {reevaluate.isPending ? "Checking…" : "Check again now"}
      </button>
    </>
  );
}

/** useOpenAlertCount feeds the section heading's count. */
export function useOpenAlertCount(): number {
  const alerts = useQuery({
    queryKey: ["listAlerts"],
    queryFn: () => alertClient.listAlerts({}),
    refetchInterval: 5 * 60 * 1000,
  });
  return alerts.data?.alerts.length ?? 0;
}

function AlertRow({
  alert, onAcknowledge, acknowledging,
}: {
  alert: Alert;
  onAcknowledge: () => void;
  acknowledging: boolean;
}) {
  const tone = severityTone(alert.severity);
  const opened = alert.openedAt ? new Date(Number(alert.openedAt.seconds) * 1000) : null;
  const target = linkFor(alert);

  return (
    <li className={`rounded-lg border ${tone.border} ${tone.bg} px-5 py-4`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <p className={`text-sm font-medium ${tone.text}`}>{alert.title}</p>
          <p className="mt-1 text-sm text-fg-muted">{alert.detail}</p>
          <p className="mt-2 text-xs text-fg-subtle">
            {opened && <>Since {opened.toLocaleDateString()}</>}
            {alert.acknowledgedAt && (
              <> · seen{alert.acknowledgedByName ? ` by ${alert.acknowledgedByName}` : ""}</>
            )}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-3 text-xs">
          {target && (
            <Link to={target} className="text-accent hover:underline">
              Open
            </Link>
          )}
          {!alert.acknowledgedAt && (
            <button
              onClick={onAcknowledge}
              disabled={acknowledging}
              className="text-fg-muted hover:text-fg disabled:opacity-50"
              title="Records that you've seen this. It stays until the condition clears."
            >
              Mark seen
            </button>
          )}
        </div>
      </div>
    </li>
  );
}

function severityTone(s: AlertSeverity) {
  switch (s) {
    case AlertSeverity.CRITICAL:
      return { border: "border-danger/40", bg: "bg-danger/10", text: "text-danger-fg" };
    case AlertSeverity.WARNING:
      return { border: "border-warning/40", bg: "bg-warning/10", text: "text-fg" };
    default:
      return { border: "border-border", bg: "bg-surface-2", text: "text-fg" };
  }
}

// Where to send someone who wants to act on it. Only kinds with an
// obvious destination get a link; a link that lands on a list and leaves
// the reader to find the row is worse than no link.
function linkFor(a: Alert): string | null {
  switch (a.kind) {
    case AlertKind.FILING_DUE:
    case AlertKind.FILING_OVERDUE:
      return "/b266";
    case AlertKind.STAMPS_LOW:
      return "/stamps";
    case AlertKind.FERMENTATION_STALLED:
      return a.entityId ? `/fermentations/${a.entityId}` : "/fermentations";
    case AlertKind.BARREL_UNMEASURED:
      return a.entityId ? `/barrels/${a.entityId}` : "/barrels";
    case AlertKind.REDISTILLATION_OPEN:
      return "/bulk";
    case AlertKind.WORK_ORDER_OVERDUE:
    case AlertKind.WORK_ORDER_UNASSIGNED:
      return "/work";
    case AlertKind.LICENCE_EXPIRING:
    case AlertKind.LICENCE_EXPIRED:
    case AlertKind.LICENCE_SECURITY_EXPIRING:
      return "/settings";
    default:
      return null;
  }
}
