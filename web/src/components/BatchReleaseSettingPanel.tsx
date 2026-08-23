import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { tenantClient } from "@/lib/clients";
import { OwnerOnly } from "@/lib/role";

/**
 * Whether a packaged lot has to be released before it can be removed.
 *
 * Off by default, and that is a decision rather than an oversight. A
 * one-person distillery that signs off by looking at the bottle should
 * not be blocked by a workflow built for a QA department, and a system
 * that forces the ceremony gets the ceremony performed rather than
 * meant.
 *
 * A *hold* is honoured either way. Holding a lot is an explicit act by a
 * named person saying this stock must not go; respecting it only when
 * this switch happens to be on would make the act meaningless.
 */
export function BatchReleaseSettingPanel() {
  const qc = useQueryClient();
  const tenant = useQuery({
    queryKey: ["getTenant"],
    queryFn: () => tenantClient.getTenant({}),
  });
  const set = useMutation({
    mutationFn: (required: boolean) => tenantClient.setBatchReleaseRequired({ required }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["getTenant"] }),
  });

  const required = tenant.data?.tenant?.requireBatchRelease ?? false;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Batch release</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <OwnerOnly>
          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={required}
              disabled={set.isPending}
              onChange={(e) => set.mutate(e.target.checked)}
              className="mt-1"
            />
            <span>
              <span className="text-fg">
                Require a lot to be released before it can be removed.
              </span>
              <span className="mt-1 block text-fg-muted">
                Somebody has to sign the lot off — and say what they checked — before
                any of it can leave. Not a CRA requirement; the Excise Act doesn't care
                whether your methanol came back. It's the control every food safety
                programme assumes exists, and the difference between a recall you can
                bound and one you can't.
              </span>
              <span className="mt-1 block text-fg-subtle">
                Putting a lot on hold stops it leaving whether this is on or not.
              </span>
            </span>
          </label>
        </OwnerOnly>
      </div>
    </section>
  );
}
