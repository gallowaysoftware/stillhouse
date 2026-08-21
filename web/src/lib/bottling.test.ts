import { describe, expect, it } from "vitest";

import { bottledLAA, bottlingLossLAA, estimateDraw } from "./bottling";

// These mirror backend/internal/rpc/bottling.go. The point of testing them
// here is that the two must agree: the form tells the operator what the
// run will take out of the tank, and if the form and the server disagree
// the operator is deciding on a number the system will not honour.

describe("estimateDraw", () => {
  it("draws less than it bottles when the source is stronger than the product", () => {
    // 1000 x 750 mL at 40% = 750 L bottled, 300 LAA. From a 60% tank that
    // is 500 L drawn — the other 250 L is the water added on the way.
    // The form used to show 750 L under "Will draw N L from <tank>", a
    // 50% overstatement at the moment the operator is judging whether the
    // tank holds enough.
    const d = estimateDraw({
      bottleCount: 1000, bottleSizeMl: 750, targetAbvPct: 40,
      bottlingLossL: 0, sourceAbvPct: 60,
    })!;
    expect(d.bottledVolumeL).toBe(750);
    expect(d.requiredLAA).toBeCloseTo(300, 9);
    expect(d.drawnVolumeL).toBeCloseTo(500, 9);
    expect(d.drawnVolumeL!).toBeLessThan(d.bottledVolumeL);
  });

  it("draws exactly what it bottles when source and product strengths match", () => {
    const d = estimateDraw({
      bottleCount: 100, bottleSizeMl: 750, targetAbvPct: 40,
      bottlingLossL: 0, sourceAbvPct: 40,
    })!;
    expect(d.drawnVolumeL).toBeCloseTo(d.bottledVolumeL, 9);
  });

  it("charges filler loss at the product's strength, and draws it at the source's", () => {
    // 2 L lost at 40% = 0.8 LAA, which must come out of a 60% tank as
    // 1.333 L — not as 2 L.
    const withLoss = estimateDraw({
      bottleCount: 1000, bottleSizeMl: 750, targetAbvPct: 40,
      bottlingLossL: 2, sourceAbvPct: 60,
    })!;
    expect(withLoss.requiredLAA).toBeCloseTo(300.8, 9);
    expect(withLoss.drawnVolumeL! - 500).toBeCloseTo(0.8 / 60 * 100, 9);
  });

  it("says nothing rather than zero when the source has no recorded strength", () => {
    const d = estimateDraw({
      bottleCount: 100, bottleSizeMl: 750, targetAbvPct: 40, bottlingLossL: 0,
    })!;
    expect(d.drawnVolumeL).toBeNull();
    expect(d.bottledVolumeL).toBe(75);
  });

  it("refuses nonsense rather than producing a confident number", () => {
    for (const args of [
      { bottleCount: 0, bottleSizeMl: 750, targetAbvPct: 40, bottlingLossL: 0 },
      { bottleCount: NaN, bottleSizeMl: 750, targetAbvPct: 40, bottlingLossL: 0 },
      { bottleCount: 100, bottleSizeMl: 0, targetAbvPct: 40, bottlingLossL: 0 },
      { bottleCount: 100, bottleSizeMl: 750, targetAbvPct: NaN, bottlingLossL: 0 },
    ]) {
      expect(estimateDraw(args)).toBeNull();
    }
  });
});

describe("bottledLAA", () => {
  it("matches the backend's arithmetic", () => {
    // backend: float64(bottleCount) * float64(bottleSizeMl) / 1000 * abv/100
    expect(bottledLAA(1000, 750, 40)).toBeCloseTo(300, 9);
    expect(bottledLAA(100, 355, 5)).toBeCloseTo(1.775, 9);
    expect(bottlingLossLAA(2, 40)).toBeCloseTo(0.8, 9);
  });
});
