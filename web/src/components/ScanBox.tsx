import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ConnectError } from "@connectrpc/connect";

import { labelClient } from "@/lib/clients";
import { LabelKind, LabelTarget } from "@/gen/stillhouse/v1/label_pb";

// Where a scan goes.
export function routeFor(t: LabelTarget): string | null {
  switch (t.kind) {
    case LabelKind.BARREL:
      return `/barrels/${t.id}`;
    case LabelKind.CONTAINER:
      return `/bulk/${t.id}`;
    case LabelKind.LOT:
      return `/bottling?lot=${t.id}`;
    case LabelKind.SHIPMENT:
      return `/sales?shipment=${t.id}`;
    case LabelKind.PRODUCT:
      return `/products?product=${t.id}`;
    default:
      return null;
  }
}

// A wedge scanner is a keyboard that types fast and presses Enter. That
// makes "scan to find" an input and a submit — no device integration, no
// permissions prompt, and it works with a phone camera app that pastes,
// with a bluetooth ring scanner, and with fingers.
//
// Bound to a keyboard shortcut rather than sitting in the chrome, because
// the thing an operator does is scan first and look second: they are
// holding a scanner, not a mouse.
export function ScanBox() {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [choices, setChoices] = useState<LabelTarget[]>([]);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const el = document.activeElement;
      const typing =
        el instanceof HTMLInputElement ||
        el instanceof HTMLTextAreaElement ||
        el instanceof HTMLSelectElement;
      if (e.key === "Escape") {
        setOpen(false);
        return;
      }
      // Slash is the convention and costs nothing; Ctrl-K for the people
      // who expect it. Neither steals a keystroke from a form.
      if (!typing && (e.key === "/" || (e.key.toLowerCase() === "k" && (e.ctrlKey || e.metaKey)))) {
        e.preventDefault();
        setOpen(true);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (open) {
      setError(null);
      setChoices([]);
      setValue("");
      // Focus after paint, or the scanner's first characters land nowhere.
      requestAnimationFrame(() => input.current?.focus());
    }
  }, [open]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const scanned = value.trim();
    if (!scanned) return;
    setBusy(true);
    setError(null);
    setChoices([]);
    try {
      const res = await labelClient.resolveLabel({ scanned });
      if (res.ambiguous.length > 0) {
        setChoices(res.ambiguous);
        return;
      }
      const to = res.target ? routeFor(res.target) : null;
      if (!to) {
        setError("Nothing to open for that.");
        return;
      }
      setOpen(false);
      navigate(to);
    } catch (err) {
      setError(err instanceof ConnectError ? err.rawMessage : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button
        data-print-hide
        onClick={() => setOpen(true)}
        title="Scan a cask tag or case label  (/)"
        className="rounded border border-border-strong px-2 py-1 text-xs text-fg-muted hover:text-fg"
      >
        Scan
      </button>
    );
  }

  return (
    <div
      data-print-hide
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-32"
      onMouseDown={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
    >
      <div className="w-full max-w-lg rounded-lg border border-border bg-surface-2 p-5 shadow-lg">
        <form onSubmit={submit}>
          <label className="mb-1 block text-xs text-fg-muted">
            Scan a tag, or type a code, a lot number or a cask name
          </label>
          <input
            ref={input}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="B3Y984W17RJGEK"
            className="w-full rounded border border-border-strong px-3 py-2 font-mono text-sm"
          />
          <div className="mt-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={busy}
              className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {busy ? "Looking…" : "Find it"}
            </button>
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="text-sm text-fg-muted hover:text-fg"
            >
              Cancel
            </button>
            {error && <span className="text-sm text-danger-fg">{error}</span>}
          </div>
        </form>

        {choices.length > 0 && (
          <div className="mt-4 border-t border-border pt-3">
            <p className="mb-2 text-xs text-fg-muted">
              More than one thing answers to that. Which did you mean?
            </p>
            <ul className="space-y-1">
              {choices.map((t) => {
                const to = routeFor(t);
                return (
                  <li key={`${t.kind}-${t.id}`}>
                    <button
                      onClick={() => { if (to) { setOpen(false); navigate(to); } }}
                      className="w-full rounded px-2 py-1 text-left text-sm hover:bg-surface-3"
                    >
                      <span className="text-fg">{t.title}</span>{" "}
                      <span className="text-xs text-fg-muted">{t.subtitle}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
