import { useQuery } from "@tanstack/react-query";

type BuildInfo = {
  version: string;
  commit: string;
  build_date: string;
  release: boolean;
};

/**
 * What is running.
 *
 * A hosted install tracks a tagged release, and until stage 156 there was
 * no way to tell from the outside which one — the operator who restarts
 * the stack and the person asking whether a fix has landed both had to
 * reason from a container digest. `/version` is served unauthenticated by
 * the same binary that serves everything else, so what it reports is
 * necessarily what is answering requests.
 *
 * A build that reports "dev" is somebody's working tree rather than a
 * release, and says so plainly: on a hosted install that is a finding.
 */
export function BuildPanel() {
  const build = useQuery({
    queryKey: ["buildInfo"],
    queryFn: async (): Promise<BuildInfo> => {
      const resp = await fetch("/version");
      if (!resp.ok) throw new Error(`/version returned ${resp.status}`);
      return resp.json() as Promise<BuildInfo>;
    },
    staleTime: Infinity,
  });

  if (build.isPending || build.error || !build.data) return null;
  const b = build.data;
  const built = b.build_date ? new Date(b.build_date) : null;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">This install</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <dl className="grid grid-cols-[auto,1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="text-fg-muted">Version</dt>
          <dd className="font-mono text-fg">{b.version}</dd>
          {b.commit && (
            <>
              <dt className="text-fg-muted">Commit</dt>
              <dd className="font-mono text-xs text-fg-muted">{b.commit}</dd>
            </>
          )}
          {built && !isNaN(built.getTime()) && (
            <>
              <dt className="text-fg-muted">Built</dt>
              <dd className="text-fg-muted">{built.toLocaleString()}</dd>
            </>
          )}
        </dl>
        {!b.release && (
          <p className="mt-4 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg-muted">
            This build is not a tagged release. That's normal on a development
            machine; on an install anyone relies on it means the running image
            can't be matched to a version, so there's nothing to pin or roll
            back to. Cut one with <span className="font-mono text-fg">make release</span>.
          </p>
        )}
      </div>
    </section>
  );
}
