import { useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { labelClient } from "@/lib/clients";
import { LabelKind } from "@/gen/stillhouse/v1/label_pb";

// Scan the case in your hand instead of finding it in a list of ninety.
//
// `expect` is set to LOT, so scanning a cask tag here is refused with the
// reason — "that is a cask, not a packaged lot" — rather than navigating
// away from a half-built pick. The refusal is the feature: a picker who
// grabbed the wrong pallet wants to be told, not redirected.
export function ScanToPick({ onPicked }: { onPicked: (lotId: string) => void }) {
  const [value, setValue] = useState("");
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function resolve() {
    const scanned = value.trim();
    if (!scanned) return;
    setBusy(true);
    setNote(null);
    try {
      const res = await labelClient.resolveLabel({ scanned, expect: LabelKind.LOT });
      if (res.ambiguous.length > 0) {
        setNote({ ok: false, text: "More than one lot answers to that — pick it from the list." });
        return;
      }
      if (!res.target) {
        setNote({ ok: false, text: "No lot here is labelled that." });
        return;
      }
      onPicked(res.target.id);
      setNote({ ok: true, text: `${res.target.title} — ${res.target.subtitle}` });
      setValue("");
    } catch (err) {
      setNote({ ok: false, text: err instanceof ConnectError ? err.rawMessage : String(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mb-2">
      <input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          // A wedge scanner ends with Enter. Swallow it, or the enclosing
          // form submits a half-filled pick line.
          if (e.key === "Enter") {
            e.preventDefault();
            void resolve();
          }
        }}
        disabled={busy}
        placeholder="Scan the case label…"
        className="w-full rounded border border-dashed border-border-strong px-2 py-1.5 font-mono text-sm"
      />
      {note && (
        <p className={`mt-1 text-xs ${note.ok ? "text-success-fg" : "text-danger-fg"}`}>
          {note.text}
        </p>
      )}
    </div>
  );
}
