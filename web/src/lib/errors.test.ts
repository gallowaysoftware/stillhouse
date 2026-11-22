import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";

import { mayHaveBeenSaved, retryRead, writeErrorText } from "./errors";
import { queryClient } from "./queryClient";

describe("a write that failed", () => {
  it("may have been saved when nothing on the server answered", () => {
    for (const e of [new TypeError("Failed to fetch"), ConnectError.from(new TypeError("Failed to fetch")),
      new ConnectError("504", Code.Unavailable), new ConnectError("late", Code.DeadlineExceeded)]) {
      expect(mayHaveBeenSaved(e)).toBe(true);
      expect(writeErrorText(e)).toMatch(/may have been saved/);
    }
  });
  it("was not saved when the server refused it", () => {
    const e = new ConnectError("not enough on-hand stock in that lot", Code.FailedPrecondition);
    expect(mayHaveBeenSaved(e)).toBe(false);
    expect(writeErrorText(e)).toBe("not enough on-hand stock in that lot");
  });
  it("is never sent again by itself", () => {
    expect(queryClient.getDefaultOptions().mutations?.retry).toBe(false);
  });
  it("leaves reads one retry", () => {
    expect(retryRead(0, new ConnectError("x", Code.Unavailable))).toBe(true);
    expect(retryRead(1, new ConnectError("x", Code.Unavailable))).toBe(false);
    expect(retryRead(0, new ConnectError("x", Code.NotFound))).toBe(false);
  });
});
