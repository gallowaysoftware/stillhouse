import { ReactNode } from "react";

export type CalloutTone = "info" | "success" | "warning" | "danger";

/**
 * Callout — semantic banner for inline messages. Uses a thick left
 * border in the tone's color plus a tinted background; the eye picks
 * up state at a glance before reading text. Drop in anywhere we used
 * to write `<p className="text-xs text-warning-fg">…</p>` ad-hoc.
 *
 * Tone meanings (kept consistent across the app):
 *   info     — neutral hint, no action needed
 *   success  — confirmation that something completed cleanly
 *   warning  — operator should look, but nothing is broken yet
 *   danger   — action required; data integrity or compliance at risk
 */
export function Callout({
  tone = "info",
  title,
  children,
}: {
  tone?: CalloutTone;
  title?: string;
  children: ReactNode;
}) {
  const styles = toneStyles(tone);
  return (
    <div className={`rounded-md border border-l-4 px-3 py-2 text-sm ${styles}`}>
      {title && <p className="mb-0.5 font-medium">{title}</p>}
      <div className="text-fg-muted">{children}</div>
    </div>
  );
}

function toneStyles(tone: CalloutTone): string {
  switch (tone) {
    case "success":
      return "border-success/40 border-l-success bg-success/10 text-success-fg";
    case "warning":
      return "border-warning/40 border-l-warning bg-warning/10 text-warning-fg";
    case "danger":
      return "border-danger/40 border-l-danger bg-danger/10 text-danger-fg";
    default:
      return "border-info/40 border-l-info bg-info/10 text-info-fg";
  }
}
