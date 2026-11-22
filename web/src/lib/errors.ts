import { Code, ConnectError } from "@connectrpc/connect";

// A proxy's 502, 503 and 504 arrive as Unavailable; a dropped connection as
// Unknown. The server itself answers neither.
const unanswered = new Set<Code>([Code.Unavailable, Code.DeadlineExceeded, Code.Aborted, Code.Unknown]);

/** Whether a failed write could have reached the record all the same. */
export function mayHaveBeenSaved(error: unknown): boolean {
  return !(error instanceof ConnectError) || unanswered.has(error.code);
}

/** Reads are safe to repeat once when nothing answered. */
export function retryRead(failures: number, error: unknown): boolean {
  return failures < 1 && mayHaveBeenSaved(error);
}

export function writeErrorText(error: unknown): string {
  if (mayHaveBeenSaved(error)) {
    return "No answer came back. It may have been saved: check the list before entering it again.";
  }
  return (error as ConnectError).rawMessage;
}

export function readErrorText(error: unknown): string {
  if (mayHaveBeenSaved(error)) return "Stillhouse could not be reached.";
  return (error as ConnectError).rawMessage;
}
