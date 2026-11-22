import { Callout } from "@/components/Callout";
import { readErrorText } from "@/lib/errors";

/** A load that failed, which is not the same as there being nothing. */
export function LoadError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <Callout tone="danger" title="Not loaded">
      {readErrorText(error)}{" "}
      <button type="button" className="underline" onClick={onRetry}>
        Try again
      </button>
    </Callout>
  );
}
