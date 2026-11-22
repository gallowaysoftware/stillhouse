export type ToolCall = { id: string; type: "function"; function: { name: string; arguments: string } };
export type ContentPart = { type: "text"; text: string } | { type: "image_url"; image_url: { url: string } };
export type ChatMessage = {
  role: "user" | "assistant" | "tool";
  content?: string | ContentPart[];
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  is_error?: boolean;
  // Page-only: something the page told the model, not the operator.
  hidden?: boolean;
  // Page-only: the entry the operator undid.
  removed?: string;
};

export type EntryKind =
  | "mash_ingredient" | "mash_reading" | "fermentation_reading"
  | "distillation_charge" | "distillation_reading" | "distillation_cut";

const recorded: Record<string, { field: string; kind: EntryKind }> = {
  add_mash_ingredient: { field: "usage", kind: "mash_ingredient" },
  add_mash_reading: { field: "metric", kind: "mash_reading" },
  add_fermentation_reading: { field: "log", kind: "fermentation_reading" },
  add_distillation_charge: { field: "charge", kind: "distillation_charge" },
  add_distillation_reading: { field: "reading", kind: "distillation_reading" },
  add_distillation_cut: { field: "cut", kind: "distillation_cut" },
};

/** The entry a tool result recorded, if it is one the operator can undo. */
export function recordedEntry(tool: string, result: string): { kind: EntryKind; id: string } | null {
  const r = recorded[tool];
  if (!r) return null;
  try {
    const id: unknown = JSON.parse(result)[r.field]?.id;
    return typeof id === "string" && id !== "" ? { kind: r.kind, id } : null;
  } catch {
    return null;
  }
}

/** Entries already taken back, by the operator or by the assistant. */
export function removedEntries(messages: ChatMessage[]): Set<string> {
  const failed = new Set(messages.filter((m) => m.is_error).map((m) => m.tool_call_id));
  const answered = new Set(messages.filter((m) => m.role === "tool").map((m) => m.tool_call_id));
  const gone = new Set<string>();
  for (const m of messages) {
    if (m.removed) gone.add(m.removed);
    for (const c of m.tool_calls ?? []) {
      if (c.function.name !== "remove_entry" || failed.has(c.id) || !answered.has(c.id)) continue;
      try {
        const id: unknown = JSON.parse(c.function.arguments).id;
        if (typeof id === "string") gone.add(id);
      } catch {
        // A call that never parsed removed nothing.
      }
    }
  }
  return gone;
}

const reads = /^(get_|list_|plan_)/;

export function isRead(tool: string): boolean {
  return reads.test(tool);
}

/** What a call said, in the order it said it, without the empty ones. */
export function callSummary(args: string): string {
  try {
    return Object.entries(JSON.parse(args || "{}") as Record<string, unknown>)
      .filter(([, v]) => v !== "" && v !== null)
      .map(([k, v]) => {
        const name = k.replace(/_run_id$/, "").replace(/_/g, " ");
        const value = typeof v === "object" ? JSON.stringify(v) : String(v);
        return value.startsWith(name) ? value : `${name} ${value}`;
      })
      .join(" · ");
  } catch {
    return args;
  }
}

/** Splits streamed text into whole lines and whatever is left over. */
export function takeLines(buffer: string): { lines: string[]; rest: string } {
  const parts = buffer.split("\n");
  const rest = parts.pop() ?? "";
  return { lines: parts.filter((l) => l.trim() !== ""), rest };
}

// The server keeps the last sixty; what is stored here is the same length.
const KEEP = 60;

/** What is kept between visits: photos are dropped, the words stay. */
export function forStorage(messages: ChatMessage[]): ChatMessage[] {
  return messages.slice(-KEEP).map((m) =>
    Array.isArray(m.content)
      ? { ...m, content: m.content.map((p): ContentPart => (p.type === "image_url" ? { type: "text", text: "(a photo)" } : p)) }
      : m,
  );
}

const storageKey = (userId: string) => `stillhouse_assistant_${userId}`;

export function loadConversation(userId: string): ChatMessage[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(storageKey(userId)) ?? "[]");
    return Array.isArray(v) ? (v as ChatMessage[]) : [];
  } catch {
    return [];
  }
}

export function saveConversation(userId: string, messages: ChatMessage[]) {
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(forStorage(messages)));
  } catch {
    // Full or blocked storage loses the chat on reload, not the entries.
  }
}

const MAX_EDGE = 1600;

/** Scales a photo to what reads a label, and to what fits one request. */
export async function photoDataURL(file: File): Promise<string> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  canvas.getContext("2d")!.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  return canvas.toDataURL("image/jpeg", 0.85);
}
