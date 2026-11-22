import { describe, expect, it } from "vitest";

import { conversationFor, Conversation } from "./assistantStore";

const line = (v: unknown) => new TextEncoder().encode(JSON.stringify(v) + "\n");

describe("a conversation", () => {
  it("keeps what a turn records when nobody is looking at it", async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({ start: (c) => { stream = c; } });
    const c = conversationFor("u1");
    const leave = c.subscribe(() => undefined);
    c.set({ draft: "SG 1.020" });
    const turn = c.send(() => undefined, async () => new Response(body));
    leave();
    stream.enqueue(line({ message: { role: "assistant", tool_calls: [{ id: "1", type: "function", function: { name: "add_fermentation_reading", arguments: "{}" } }] } }));
    stream.enqueue(line({ message: { role: "tool", tool_call_id: "1", content: '{"log":{"id":"x"}}' } }));
    stream.enqueue(line({ done: true }));
    stream.close();
    await turn;
    const back = conversationFor("u1").getState();
    expect(back.messages.map((m) => m.role)).toEqual(["user", "assistant", "tool"]);
    expect(back).toMatchObject({ busy: false, error: "", draft: "" });
  });

  it("gives the words back when the turn was refused", async () => {
    const c = new Conversation("u2");
    c.set({ draft: "pitched 100 g", photos: ["data:a"] });
    const turn = c.send(() => undefined, async () => new Response("authentication required", { status: 401 }));
    c.set({ draft: "and stirred" });
    await turn;
    expect(c.getState()).toMatchObject({
      messages: [], draft: "pitched 100 g\nand stirred", photos: ["data:a"], busy: false, error: "authentication required",
    });
  });

  it("says some may be recorded when the stream stops early", async () => {
    const c = new Conversation("u3");
    c.set({ draft: "x" });
    await c.send(() => undefined, async () => new Response(new ReadableStream({ start: (s) => s.close() })));
    expect(c.getState().error).toMatch(/may have been recorded/);
    expect(c.getState().messages).toHaveLength(1);
  });
});
