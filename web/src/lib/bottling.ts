/**
 * Bottling arithmetic, kept in one place and mirrored from
 * backend/internal/rpc/bottling.go.
 *
 * Two different volumes are involved and conflating them is the bug this
 * module exists to prevent: the volume that ends up in bottles, and the
 * volume drawn out of the tank. They are only equal when the source is
 * already at the product's strength. Bottling 40% product from a 60%
 * cask-strength tank draws two thirds of the bottled volume, the rest
 * being the water added on the way.
 *
 * The form used to show bottled volume under the label "Will draw N L
 * from <tank>" — a 50% overstatement at the exact moment the operator is
 * deciding whether the tank holds enough.
 */

/** Absolute alcohol in a run's bottles, in litres. */
export function bottledLAA(bottleCount: number, bottleSizeMl: number, targetAbvPct: number): number {
  return (bottleCount * bottleSizeMl) / 1000 * (targetAbvPct / 100);
}

/** Absolute alcohol lost at the filler, charged at the product's strength. */
export function bottlingLossLAA(bottlingLossL: number, targetAbvPct: number): number {
  return bottlingLossL * (targetAbvPct / 100);
}

export type DrawEstimate = {
  /** Litres that will end up in bottles. */
  bottledVolumeL: number;
  /** Absolute alcohol the run needs, bottles plus filler loss. */
  requiredLAA: number;
  /**
   * Litres drawn from the source. null when the source has no recorded
   * strength — the figure is undefined rather than zero, and showing "0 L"
   * would be worse than showing nothing.
   */
  drawnVolumeL: number | null;
};

export function estimateDraw(args: {
  bottleCount: number;
  bottleSizeMl: number;
  targetAbvPct: number;
  bottlingLossL: number;
  sourceAbvPct?: number;
}): DrawEstimate | null {
  const { bottleCount, bottleSizeMl, targetAbvPct, bottlingLossL, sourceAbvPct } = args;
  if (!Number.isFinite(bottleCount) || bottleCount <= 0) return null;
  if (!Number.isFinite(bottleSizeMl) || bottleSizeMl <= 0) return null;
  if (!Number.isFinite(targetAbvPct) || targetAbvPct <= 0) return null;

  const loss = Number.isFinite(bottlingLossL) && bottlingLossL > 0 ? bottlingLossL : 0;
  const bottledVolumeL = (bottleCount * bottleSizeMl) / 1000;
  const requiredLAA = bottledLAA(bottleCount, bottleSizeMl, targetAbvPct) + bottlingLossLAA(loss, targetAbvPct);

  const drawnVolumeL =
    sourceAbvPct !== undefined && Number.isFinite(sourceAbvPct) && sourceAbvPct > 0
      ? (requiredLAA / sourceAbvPct) * 100
      : null;

  return { bottledVolumeL, requiredLAA, drawnVolumeL };
}
