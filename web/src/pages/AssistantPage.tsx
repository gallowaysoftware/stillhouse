import { ChangeEvent, FormEvent, KeyboardEvent, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { Shell } from "@/components/Shell";
import { distillationClient, fermentationClient, mashClient, userClient } from "@/lib/clients";
import { useT } from "@/lib/i18n";
import {
  callSummary, isRead, loadConversation, photoDataURL, recordedEntry, removedEntries, saveConversation, takeLines,
  type ChatMessage, type ContentPart, type EntryKind, type ToolCall,
} from "@/lib/assistant";

const remove: Record<EntryKind, (id: string) => Promise<unknown>> = {
  mash_ingredient: (id) => mashClient.deleteMashIngredient({ id }),
  mash_reading: (id) => mashClient.deleteMashMetric({ id }),
  fermentation_reading: (id) => fermentationClient.deleteFermentationLog({ id }),
  distillation_charge: (id) => distillationClient.deleteDistillationCharge({ id }),
  distillation_reading: (id) => distillationClient.deleteDistillationReading({ id }),
  distillation_cut: (id) => distillationClient.deleteDistillationCut({ id }),
};

type Event = { message?: ChatMessage; error?: string; done?: boolean };

export function AssistantPage() {
  const me = useQuery({ queryKey: ["getMe"], queryFn: () => userClient.getMe({}) });
  const status = useQuery({
    queryKey: ["assistantStatus"],
    queryFn: async () => {
      const r = await fetch("/api/assistant/status", { credentials: "include" });
      if (!r.ok) throw new Error(await r.text());
      return (await r.json()) as { enabled: boolean; model: string };
    },
  });
  const t = useT();
  const userId = me.data?.user?.id;
  return (
    <Shell>
      <h1 className="mb-4 text-3xl font-bold tracking-tight">{t("Assistant", "Assistant")}</h1>
      {status.data && !status.data.enabled && (
        <Callout tone="info" title="Not configured">
          Set STILLHOUSE_ASSISTANT_URL and STILLHOUSE_ASSISTANT_MODEL on the server to a model you host. What you
          type and photograph is sent there.
        </Callout>
      )}
      {status.error && <Callout tone="danger">{String(status.error)}</Callout>}
      {status.data?.enabled && userId && <Conversation key={userId} userId={userId} model={status.data.model} />}
    </Shell>
  );
}

function Conversation({ userId, model }: { userId: string; model: string }) {
  const qc = useQueryClient();
  const [messages, setMessages] = useState<ChatMessage[]>(() => loadConversation(userId));
  const [draft, setDraft] = useState("");
  const [photos, setPhotos] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => saveConversation(userId, messages), [userId, messages]);
  // Braced: newer browsers return a promise from scrollIntoView, and React
  // would call it as the effect's cleanup.
  useEffect(() => { end.current?.scrollIntoView({ block: "start" }); }, [messages, busy]);

  const results = new Map(messages.filter((m) => m.role === "tool").map((m) => [m.tool_call_id, m]));
  const undone = removedEntries(messages);
  const calls = new Map(messages.flatMap((m) => m.tool_calls ?? []).map((c) => [c.id, c]));

  async function send(e?: FormEvent) {
    e?.preventDefault();
    const words = draft.trim();
    if (busy || (!words && photos.length === 0)) return;
    const content: string | ContentPart[] = photos.length
      ? [{ type: "text", text: words || "(photo)" }, ...photos.map((url): ContentPart => ({ type: "image_url", image_url: { url } }))]
      : words;
    const next: ChatMessage[] = [...messages, { role: "user", content }];
    setMessages(next);
    setDraft("");
    setPhotos([]);
    setError("");
    setBusy(true);
    let finished = false;
    try {
      const resp = await fetch("/api/assistant/chat", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          messages: next.slice(-60).map(({ role, content, tool_calls, tool_call_id }) => ({ role, content, tool_calls, tool_call_id })),
          time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        }),
      });
      if (!resp.ok || !resp.body) {
        // Refused before the turn began, so nothing was recorded and the
        // words go back where they can be sent again.
        setMessages(messages);
        setDraft(words);
        setPhotos(photos);
        finished = true;
        throw new Error(await resp.text());
      }
      const reader = resp.body.pipeThrough(new TextDecoderStream()).getReader();
      let buffer = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        const { lines, rest } = takeLines(buffer + value);
        buffer = rest;
        for (const line of lines) {
          const ev = JSON.parse(line) as Event;
          if (ev.message) setMessages((ms) => [...ms, ev.message!]);
          if (ev.error) setError(ev.error);
          if (ev.done || ev.error) finished = true;
        }
      }
      if (!finished) throw new Error();
    } catch (err) {
      setError(
        finished && err instanceof Error
          ? err.message
          : "The connection dropped. Some of that may have been recorded; ask me what is on the run before repeating it.",
      );
    } finally {
      setBusy(false);
      // Anything on another page may have moved.
      void qc.invalidateQueries();
    }
  }

  async function undo(kind: EntryKind, id: string, tool: string) {
    setError("");
    try {
      await remove[kind](id);
    } catch (err) {
      setError(err instanceof ConnectError ? err.rawMessage : String(err));
      return;
    }
    setMessages((ms) => [...ms, { role: "user", hidden: true, removed: id, content: `(I removed the ${tool} entry ${id} myself. It is gone; do not record it again unless I say so.)` }]);
    void qc.invalidateQueries();
  }

  async function addPhotos(e: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files ?? []).slice(0, 4 - photos.length);
    e.target.value = "";
    try {
      const urls = await Promise.all(files.map(photoDataURL));
      setPhotos((p) => [...p, ...urls].slice(0, 4));
    } catch {
      setError("That photo could not be read.");
    }
  }

  function onKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-3 pb-40">
        {messages.length === 0 && (
          <p className="text-sm text-fg-muted">
            Say what just happened: &ldquo;6 kilos of pale malt in, lot PM-4471&rdquo;, &ldquo;mash temp 64, pH 5.4&rdquo;,
            or send a photo of a label or a gauge. Each thing recorded is shown here with an undo.
          </p>
        )}
        {messages.map((m, i) => (
          <MessageView key={i} message={m} results={results} calls={calls} undone={undone} onUndo={undo} />
        ))}
        {busy && <p className="animate-pulse text-sm text-fg-muted">Working…</p>}
        {error && <Callout tone="danger">{error}</Callout>}
        <div ref={end} />
      </div>

      <form onSubmit={send} className="fixed inset-x-0 bottom-0 z-20 border-t border-border bg-surface px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] md:left-60 md:px-8">
        {photos.length > 0 && (
          <div className="mb-2 flex gap-2">
            {photos.map((url, i) => (
              <button key={i} type="button" aria-label="Remove photo" onClick={() => setPhotos((p) => p.filter((_, j) => j !== i))}>
                <img src={url} alt="" className="h-16 w-16 rounded border border-border object-cover" />
              </button>
            ))}
          </div>
        )}
        <div className="flex items-end gap-2">
          <label className="cursor-pointer rounded border border-border-strong px-3 py-2 text-sm text-fg-muted hover:bg-surface-3">
            Photo
            <input type="file" accept="image/*" multiple className="sr-only" onChange={addPhotos} disabled={busy || photos.length >= 4} />
          </label>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={onKey}
            rows={2}
            aria-label="Message"
            placeholder="What just happened?"
            className="min-w-0 flex-1 rounded border border-border-strong bg-surface-2 px-3 py-2 text-base"
          />
          <button
            type="submit"
            disabled={busy || (!draft.trim() && photos.length === 0)}
            className="rounded bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            Send
          </button>
        </div>
        <p className="mt-2 flex justify-between text-xs text-fg-subtle">
          <span>{model}</span>
          {messages.length > 0 && !busy && (
            <button type="button" className="underline" onClick={() => setMessages([])}>
              Clear conversation
            </button>
          )}
        </p>
      </form>
    </div>
  );
}

function MessageView({
  message: m, results, calls, undone, onUndo,
}: {
  message: ChatMessage;
  results: Map<string | undefined, ChatMessage>;
  calls: Map<string, ToolCall>;
  undone: Set<string>;
  onUndo: (kind: EntryKind, id: string, tool: string) => Promise<void>;
}) {
  if (m.hidden || m.role === "tool") return null;
  if (m.role === "user") {
    const parts: ContentPart[] = typeof m.content === "string" ? [{ type: "text", text: m.content }] : (m.content ?? []);
    return (
      <div className="ml-auto max-w-[85%] rounded-lg bg-accent/15 px-3 py-2 text-fg">
        {parts.map((p, i) =>
          p.type === "text"
            ? <p key={i} className="whitespace-pre-wrap break-words">{p.text}</p>
            : <img key={i} src={p.image_url.url} alt="" className="mt-2 max-h-60 rounded" />,
        )}
      </div>
    );
  }
  return (
    <div className="flex max-w-[95%] flex-col gap-2">
      {typeof m.content === "string" && m.content.trim() !== "" && (
        <p className="whitespace-pre-wrap break-words rounded-lg bg-surface-2 px-3 py-2 text-fg shadow-sm">{m.content}</p>
      )}
      {(m.tool_calls ?? []).map((c) => {
        const result = results.get(c.id);
        const name = calls.get(c.id)?.function.name ?? c.function.name;
        const body = typeof result?.content === "string" ? result.content : "";
        if (isRead(name) && !result?.is_error) {
          return <p key={c.id} className="text-xs text-fg-subtle">looked at {name.replace(/^(get|list|plan)_/, "").replace(/_/g, " ")}</p>;
        }
        const entry = result && !result.is_error ? recordedEntry(name, body) : null;
        const gone = entry && undone.has(entry.id);
        return (
          <div key={c.id} className={`rounded-md border border-l-4 px-3 py-2 text-sm ${result?.is_error ? "border-warning/40 bg-warning/10" : "border-success/40 bg-success/10"}`}>
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className={`font-medium text-fg ${gone ? "line-through" : ""}`}>{name.replace(/_/g, " ")}</p>
                <p className={`break-words text-xs text-fg-muted ${gone ? "line-through" : ""}`}>{callSummary(c.function.arguments)}</p>
                {result?.is_error && <p className="mt-1 break-words text-xs text-warning-fg">Not recorded: {body}</p>}
                {!result && <p className="mt-1 text-xs text-fg-subtle">…</p>}
              </div>
              {entry && !gone && (
                <button type="button" onClick={() => void onUndo(entry.kind, entry.id, name)} className="shrink-0 rounded border border-border-strong px-2 py-1 text-xs text-fg-muted hover:bg-surface-3">
                  Undo
                </button>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
