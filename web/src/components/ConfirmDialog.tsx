import { FormEvent, ReactNode, useEffect, useRef, useState } from "react";

export type ConfirmTone = "danger" | "warning" | "primary";

/**
 * useConfirm — imperative-style replacement for window.prompt / window.confirm.
 * Call cases:
 *   const confirm = useConfirm();
 *   const ok = await confirm({ title: "Delete X?", tone: "danger", ... });
 *   if (ok === null) return;            // user cancelled
 *   doIt(ok.reason);                    // ok.reason is the typed value when requireReason=true
 *
 * Renders a modal that traps focus and echoes the record details + the
 * consequences of confirming. Required-reason flows surface the input
 * upfront with a labeled box instead of cramming it into a single line.
 */
export type ConfirmOptions = {
  title: string;
  /** Free-form description above the input. Echo back the record being acted on
   *  ('Void removal #142, 30 bottles will be refunded to inventory'). */
  body: ReactNode;
  /** Optional bulleted consequences — appear under the body in a tighter list. */
  consequences?: ReactNode[];
  /** When set, the user must type a reason before the confirm button enables. */
  requireReason?: { label: string; placeholder?: string };
  /** When set, the user must retype this value into a confirm input (e.g., tenant name). */
  requireTypedMatch?: { label: string; expected: string };
  /** When set, the user enters a positive integer ≤ max before confirming. */
  requireQuantity?: { label: string; max: number; defaultValue?: number };
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: ConfirmTone;
};

type ResolveValue = { reason: string; quantity?: number } | null;

let openImpl: ((opts: ConfirmOptions) => Promise<ResolveValue>) | null = null;

/** Hook callers use to open a confirm. Resolves to null on cancel,
 *  or { reason } when confirmed (reason is "" if no input field was shown). */
export function useConfirm() {
  return (opts: ConfirmOptions): Promise<ResolveValue> => {
    if (!openImpl) {
      // The provider isn't mounted — fail loudly in dev, silently in prod.
      console.error("useConfirm called before <ConfirmProvider/> mounted");
      return Promise.resolve(null);
    }
    return openImpl(opts);
  };
}

type Pending = ConfirmOptions & { resolve: (v: ResolveValue) => void };

/** Mount once near the root. Hosts the modal; useConfirm pipes requests here. */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const [reason, setReason] = useState("");
  const [match, setMatch] = useState("");
  const [quantity, setQuantity] = useState("");
  const reasonRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    openImpl = (opts) =>
      new Promise<ResolveValue>((resolve) => {
        setReason("");
        setMatch("");
        setQuantity(opts.requireQuantity?.defaultValue !== undefined ? String(opts.requireQuantity.defaultValue) : "");
        setPending({ ...opts, resolve });
      });
    return () => { openImpl = null; };
  }, []);

  useEffect(() => {
    if (pending && reasonRef.current) reasonRef.current.focus();
  }, [pending]);

  // Esc cancels.
  useEffect(() => {
    if (!pending) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") cancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending]);

  function cancel() {
    if (!pending) return;
    pending.resolve(null);
    setPending(null);
  }
  function commit(e?: FormEvent) {
    if (e) e.preventDefault();
    if (!pending) return;
    if (pending.requireTypedMatch && match !== pending.requireTypedMatch.expected) return;
    if (pending.requireReason && !reason.trim()) return;
    let qNum: number | undefined;
    if (pending.requireQuantity) {
      qNum = Number(quantity);
      if (!Number.isFinite(qNum) || qNum <= 0 || qNum > pending.requireQuantity.max) return;
    }
    pending.resolve({ reason: reason.trim(), quantity: qNum });
    setPending(null);
  }

  return (
    <>
      {children}
      {pending && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center px-4"
          role="dialog"
          aria-modal="true"
        >
          <div className="absolute inset-0 bg-black/60" onClick={cancel} aria-hidden />
          <form
            onSubmit={commit}
            className="relative w-full max-w-md rounded-xl border border-border bg-surface-2 p-6 shadow-card-dark"
          >
            <h2 className="text-base font-semibold text-fg">{pending.title}</h2>
            <div className="mt-2 text-sm text-fg-muted">{pending.body}</div>
            {pending.consequences && pending.consequences.length > 0 && (
              <ul className="mt-3 space-y-1 text-sm text-fg-muted">
                {pending.consequences.map((c, i) => (
                  <li key={i} className="flex gap-2">
                    <span className="text-fg-subtle">→</span>
                    <span>{c}</span>
                  </li>
                ))}
              </ul>
            )}
            {pending.requireTypedMatch && (
              <div className="mt-4 space-y-1">
                <label className="block text-sm font-medium text-fg">
                  {pending.requireTypedMatch.label}
                </label>
                <p className="text-xs text-fg-muted">
                  Retype <span className="font-mono text-fg">{pending.requireTypedMatch.expected}</span> to confirm.
                </p>
                <input
                  value={match}
                  onChange={(e) => setMatch(e.target.value)}
                  className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
                />
              </div>
            )}
            {pending.requireQuantity && (
              <div className="mt-4 space-y-1">
                <label className="block text-sm font-medium text-fg">
                  {pending.requireQuantity.label}
                </label>
                <p className="text-xs text-fg-muted">Max {pending.requireQuantity.max.toLocaleString()}.</p>
                <input
                  type="number"
                  min={1}
                  max={pending.requireQuantity.max}
                  value={quantity}
                  onChange={(e) => setQuantity(e.target.value)}
                  className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
                />
              </div>
            )}
            {pending.requireReason && (
              <div className="mt-4 space-y-1">
                <label className="block text-sm font-medium text-fg">
                  {pending.requireReason.label}
                </label>
                <input
                  ref={reasonRef}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder={pending.requireReason.placeholder}
                  className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
                />
              </div>
            )}
            <div className="mt-6 flex justify-end gap-2">
              <button
                type="button"
                onClick={cancel}
                className="rounded border border-border-strong px-3 py-2 text-sm font-medium text-fg hover:bg-surface-3"
              >
                {pending.cancelLabel ?? "Cancel"}
              </button>
              <button
                type="submit"
                disabled={
                  (pending.requireTypedMatch &&
                    match !== pending.requireTypedMatch.expected) ||
                  (pending.requireReason && !reason.trim()) ||
                  (pending.requireQuantity &&
                    (!Number.isFinite(Number(quantity)) ||
                      Number(quantity) <= 0 ||
                      Number(quantity) > pending.requireQuantity.max))
                }
                className={toneButtonClass(pending.tone ?? "primary")}
              >
                {pending.confirmLabel ?? "Confirm"}
              </button>
            </div>
          </form>
        </div>
      )}
    </>
  );
}

function toneButtonClass(tone: ConfirmTone): string {
  switch (tone) {
    case "danger":
      return "rounded bg-red-600 px-3 py-2 text-sm font-medium text-white hover:bg-red-500 disabled:opacity-50";
    case "warning":
      return "rounded bg-amber-500 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-amber-400 disabled:opacity-50";
    default:
      return "rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50";
  }
}
