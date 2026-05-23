import { ComponentProps, ReactNode } from "react";

/**
 * EmptyState — the "nothing here yet" surface. Use INSTEAD of an empty
 * table, not inside one. Title + helper text + optional CTA.
 *
 * Visual: outlined card with the accent dot motif from the brand, soft
 * surface so it doesn't compete with real data once it arrives.
 */
export function EmptyState({
  title,
  message,
  action,
}: {
  title: string;
  message?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-surface-2/50 px-6 py-12 text-center">
      <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-full bg-accent/10">
        <span className="block h-2 w-2 rounded-full bg-accent" />
      </div>
      <h3 className="text-base font-semibold text-fg">{title}</h3>
      {message && <p className="mx-auto mt-1 max-w-md text-sm text-fg-muted">{message}</p>}
      {action && <div className="mt-4 flex justify-center">{action}</div>}
    </div>
  );
}

/**
 * EmptyRow — drop-in replacement for the bare "No X yet" `<tr>` rows that
 * existed before. Renders an EmptyState centered inside a single
 * colSpan'd cell so the table layout doesn't have to change.
 */
export function EmptyRow({
  colSpan,
  ...props
}: { colSpan: number } & ComponentProps<typeof EmptyState>) {
  return (
    <tr>
      <td colSpan={colSpan} className="px-4 py-6">
        <EmptyState {...props} />
      </td>
    </tr>
  );
}
