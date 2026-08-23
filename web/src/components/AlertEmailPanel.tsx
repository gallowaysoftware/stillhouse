import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { alertClient, userClient } from "@/lib/clients";

/**
 * Whether this account gets emailed when an alert opens.
 *
 * Per-person rather than per-tenant: the operator who wants to know a
 * ferment stalled is not always the person who wants to know a return is
 * due, and one shared switch means somebody turns the whole thing off
 * for everyone.
 *
 * Only warnings and criticals are ever mailed. Info-level alerts stay on
 * the dashboard, because a system that emails about everything is a
 * system people filter.
 */
export function AlertEmailPanel() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["getMe"], queryFn: () => userClient.getMe({}) });
  const set = useMutation({
    mutationFn: (enabled: boolean) => alertClient.setAlertEmail({ enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["getMe"] }),
  });

  const enabled = me.data?.user?.alertEmail ?? true;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Alert email</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <label className="flex items-start gap-3 text-sm">
          <input
            type="checkbox"
            checked={enabled}
            disabled={set.isPending}
            onChange={(e) => set.mutate(e.target.checked)}
            className="mt-1"
          />
          <span>
            <span className="text-fg">Email me when something needs attention.</span>
            <span className="mt-1 block text-fg-muted">
              A return coming due or overdue, excise stamps below a week of cover, a
              fermentation that has stopped reporting. One email per condition, once —
              not once every fifteen minutes while it stays true.
            </span>
          </span>
        </label>
      </div>
    </section>
  );
}
