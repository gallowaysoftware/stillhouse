import {
  loadConversation, saveConversation, takeLines,
  type ChatMessage, type ContentPart,
} from "./assistant";

export type ConversationState = {
  messages: ChatMessage[];
  draft: string;
  photos: string[];
  busy: boolean;
  error: string;
};

type StreamEvent = { message?: ChatMessage; error?: string; done?: boolean };

/**
 * One person's conversation, held outside the page: a turn keeps running on
 * the server after its page is left, and what it records has to be there,
 * with its undo, when the operator comes back.
 */
export class Conversation {
  private state: ConversationState;
  private readonly listeners = new Set<() => void>();

  constructor(private readonly userId: string) {
    this.state = { messages: loadConversation(userId), draft: "", photos: [], busy: false, error: "" };
  }

  getState = () => this.state;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  set(patch: Partial<ConversationState>) {
    this.state = { ...this.state, ...patch };
    if (patch.messages) saveConversation(this.userId, patch.messages);
    this.listeners.forEach((l) => l());
  }

  append(message: ChatMessage) {
    this.set({ messages: [...this.state.messages, message] });
  }

  async send(onSettled: () => void, post: typeof fetch = fetch) {
    const { messages, draft, photos, busy } = this.state;
    const words = draft.trim();
    if (busy || (!words && photos.length === 0)) return;
    const content: string | ContentPart[] = photos.length
      ? [{ type: "text", text: words || "(photo)" }, ...photos.map((url): ContentPart => ({ type: "image_url", image_url: { url } }))]
      : words;
    const next: ChatMessage[] = [...messages, { role: "user", content }];
    this.set({ messages: next, draft: "", photos: [], error: "", busy: true });
    let finished = false;
    try {
      const resp = await post("/api/assistant/chat", {
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
        // words go back where they can be sent again, ahead of anything
        // typed since.
        this.set({
          messages,
          draft: [words, this.state.draft].filter(Boolean).join("\n"),
          photos: [...photos, ...this.state.photos].slice(0, 4),
        });
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
          const ev = JSON.parse(line) as StreamEvent;
          if (ev.message) this.append(ev.message);
          if (ev.error) this.set({ error: ev.error });
          if (ev.done || ev.error) finished = true;
        }
      }
      if (!finished) throw new Error();
    } catch (err) {
      this.set({
        error: finished && err instanceof Error
          ? err.message
          : "The connection dropped. Some of that may have been recorded; ask me what is on the run before repeating it.",
      });
    } finally {
      this.set({ busy: false });
      onSettled();
    }
  }
}

const conversations = new Map<string, Conversation>();

export function conversationFor(userId: string): Conversation {
  let c = conversations.get(userId);
  if (!c) {
    c = new Conversation(userId);
    conversations.set(userId, c);
  }
  return c;
}
