import { describe, expect, it } from "vitest";

import { callSummary, forStorage, isRead, recordedEntry, removedEntries, takeLines, type ChatMessage } from "./assistant";

describe("recordedEntry", () => {
  it("finds the entry each capture tool returns", () => {
    expect(recordedEntry("add_mash_ingredient", '{"usage":{"id":"a"}}')).toEqual({ kind: "mash_ingredient", id: "a" });
    expect(recordedEntry("add_distillation_cut", '{"cut":{"id":"b"}}')).toEqual({ kind: "distillation_cut", id: "b" });
  });
  it("offers no undo for anything else", () => {
    expect(recordedEntry("start_mash", '{"mash_run":{"id":"a"}}')).toBeNull();
    expect(recordedEntry("add_mash_reading", "not_found: mash run not found")).toBeNull();
    expect(recordedEntry("add_mash_reading", '{"metric":{}}')).toBeNull();
    expect(recordedEntry("add_mash_reading", "null")).toBeNull();
  });
});

describe("takeLines", () => {
  it("holds back a line that has not finished arriving", () => {
    expect(takeLines('{"a":1}\n{"b"')).toEqual({ lines: ['{"a":1}'], rest: '{"b"' });
    expect(takeLines('{"a":1}\n\n')).toEqual({ lines: ['{"a":1}'], rest: "" });
  });
});

describe("forStorage", () => {
  it("drops photos, keeps words, and bounds the length", () => {
    const photo: ChatMessage = {
      role: "user",
      content: [{ type: "text", text: "this bag" }, { type: "image_url", image_url: { url: "data:image/jpeg;base64,AAAA" } }],
    };
    expect(JSON.stringify(forStorage([photo]))).not.toContain("base64");
    expect(JSON.stringify(forStorage([photo]))).toContain("this bag");
    expect(forStorage(Array.from({ length: 100 }, () => photo))).toHaveLength(60);
  });
});

it("summarises a call in words", () => {
  expect(callSummary('{"mash_run_id":"3","kind":"mash_ph","value":0,"notes":""}')).toBe("mash 3 · kind mash_ph · value 0");
  expect(callSummary("{")).toBe("{");
});

it("knows a read from a write", () => {
  expect(isRead("get_floor")).toBe(true);
  expect(isRead("plan_strike")).toBe(true);
  expect(isRead("add_mash_reading")).toBe(false);
  expect(isRead("remove_entry")).toBe(false);
});

it("does not say the same word twice", () => {
  expect(callSummary('{"mash_run_id":"mash 2"}')).toBe("mash 2");
});

it("knows what was taken back, and that a refused removal took nothing", () => {
  const call = (id: string, entry: string): ChatMessage => ({
    role: "assistant", tool_calls: [{ id, type: "function", function: { name: "remove_entry", arguments: JSON.stringify({ id: entry }) } }],
  });
  const gone = removedEntries([
    { role: "user", hidden: true, removed: "a", content: "x" },
    call("1", "b"), { role: "tool", tool_call_id: "1", content: "{}" },
    call("2", "c"), { role: "tool", tool_call_id: "2", content: "no", is_error: true },
    call("3", "d"),
  ]);
  expect([...gone].sort()).toEqual(["a", "b"]);
});
