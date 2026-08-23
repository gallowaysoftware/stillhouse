import { describe, expect, it } from "vitest";

import { modules, moduleCount, PATTERNS, START_B, STOP, svgPath, symbols } from "./code128";

// The table is the only part of Code 128 that cannot be derived, so it is
// the part worth checking hardest. These are the standard's own
// structural rules; between them, any single mistyped digit fails at
// least one.
describe("the pattern table", () => {
  it("has one entry per symbol value", () => {
    expect(PATTERNS.length).toBe(107); // 0..106
  });

  it("gives every symbol six runs of 11 modules, and the stop seven of 13", () => {
    PATTERNS.forEach((p, v) => {
      const widths = [...p].map(Number);
      const sum = widths.reduce((a, b) => a + b, 0);
      if (v === STOP) {
        expect(`${v}:${p.length}`).toBe(`${v}:7`);
        expect(`${v}:${sum}`).toBe(`${v}:13`);
      } else {
        expect(`${v}:${p.length}`).toBe(`${v}:6`);
        expect(`${v}:${sum}`).toBe(`${v}:11`);
      }
    });
  });

  it("uses only widths 1 to 4", () => {
    PATTERNS.forEach((p, v) => {
      for (const c of p) {
        expect(`${v}:${c}`).toMatch(/^\d+:[1-4]$/);
      }
    });
  });

  // Code 128 is self-checking because every symbol has an even number of
  // bar modules. A scanner uses it; so can we.
  it("gives every symbol an even number of bar modules", () => {
    PATTERNS.forEach((p, v) => {
      const bars = [...p].filter((_, i) => i % 2 === 0).map(Number);
      const sum = bars.reduce((a, b) => a + b, 0);
      expect(`${v}:${sum % 2}`).toBe(`${v}:0`);
    });
  });

  it("has no duplicate patterns", () => {
    expect(new Set(PATTERNS).size).toBe(PATTERNS.length);
  });

  // Two anchors that are documented as bit strings rather than as widths,
  // so they are independent of the table above: Start B is 11010010000 and
  // Stop is 1100011101011.
  it("matches the published bit patterns for Start B and Stop", () => {
    expect(bits(PATTERNS[START_B])).toBe("11010010000");
    expect(bits(PATTERNS[STOP])).toBe("1100011101011");
  });
});

describe("encoding", () => {
  it("brackets the data with start, check and stop", () => {
    const s = symbols("A");
    expect(s[0]).toBe(START_B);
    expect(s[1]).toBe("A".charCodeAt(0) - 32); // 33
    expect(s[s.length - 1]).toBe(STOP);
    // check = (104 + 33 × 1) mod 103 = 34
    expect(s[s.length - 2]).toBe(34);
  });

  it("computes the check digit by position", () => {
    // "AB": (104 + 33×1 + 34×2) mod 103 = 205 mod 103 = 102
    const s = symbols("AB");
    expect(s[s.length - 2]).toBe(102);
  });

  it("encodes a Stillhouse label code", () => {
    const code = "B3Y984W17RJGEK"; // 14 characters
    const s = symbols(code);
    // start + 14 data + check + stop
    expect(s.length).toBe(17);
    // 16 symbols of 11 modules, then the stop's 13 — which already
    // includes the terminating bar.
    expect(moduleCount(code)).toBe(16 * 11 + 13);
  });

  it("starts and ends on a bar", () => {
    const m = modules("B3Y984W17RJGEK");
    expect(m.length % 2).toBe(1); // odd count => last run is a bar
  });

  it("refuses what set B cannot carry", () => {
    expect(() => symbols("café")).toThrow();
    expect(() => symbols("a\tb")).toThrow();
  });
});

describe("svg", () => {
  it("draws bars only, and its width is the module count", () => {
    const code = "B3Y984W17RJGEK";
    const { d, width } = svgPath(code);
    expect(width).toBe(moduleCount(code));
    // Three bars in each of the 16 six-run symbols, and four in the
    // seven-run stop.
    expect(d.match(/M/g)?.length).toBe(16 * 3 + 4);
  });
});

// bits renders a width pattern as the bar/space bit string the standard
// publishes, starting with a bar.
function bits(pattern: string): string {
  return [...pattern]
    .map((w, i) => (i % 2 === 0 ? "1" : "0").repeat(Number(w)))
    .join("");
}
