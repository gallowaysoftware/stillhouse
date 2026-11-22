import { describe, expect, it } from "vitest";

import { isoDay, monthBounds } from "./dates";

// vitest.config.ts runs these in Halifax, where 22:30 is already tomorrow
// in UTC. The first expectation fails if that ever stops being so.
const evening = new Date(2026, 9, 31, 22, 30);

describe("dates", () => {
  it("an evening belongs to its own day and month", () => {
    expect(evening.toISOString().slice(0, 10)).toBe("2026-11-01");
    expect(isoDay(evening)).toBe("2026-10-31");
    expect(monthBounds(evening)).toEqual({ start: "2026-10-01", end: "2026-10-31" });
  });
});
