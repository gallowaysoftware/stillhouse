import { FermentationStatus } from "@/gen/stillhouse/v1/fermentation_pb";
import { MashMetricKind, MashStatus } from "@/gen/stillhouse/v1/mash_pb";
import { MaterialKind } from "@/gen/stillhouse/v1/material_pb";
import { SpiritKind } from "@/gen/stillhouse/v1/recipe_pb";

const laaFormatter = new Intl.NumberFormat("en-CA", {
  minimumFractionDigits: 4,
  maximumFractionDigits: 4,
});

const qtyFormatter = new Intl.NumberFormat("en-CA", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
});

const pctFormatter = new Intl.NumberFormat("en-CA", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

export function formatLAA(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return laaFormatter.format(v);
}

export function formatQty(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return qtyFormatter.format(v);
}

export function formatPct(fraction: number | undefined): string {
  if (fraction === undefined || fraction === null || Number.isNaN(fraction)) return "—";
  return pctFormatter.format(fraction * 100) + "%";
}

const materialKindLabels = new Map<MaterialKind, string>([
  [MaterialKind.GRAIN, "Grain"],
  [MaterialKind.MALT, "Malt"],
  [MaterialKind.YEAST, "Yeast"],
  [MaterialKind.WATER, "Water"],
  [MaterialKind.NGS, "Neutral grain spirit"],
  [MaterialKind.BOTANICAL, "Botanical"],
  [MaterialKind.PACKAGING, "Packaging"],
  [MaterialKind.OTHER, "Other"],
]);

export function materialKindLabel(k: MaterialKind): string {
  return materialKindLabels.get(k) ?? "Unknown";
}

const spiritKindLabels = new Map<SpiritKind, string>([
  [SpiritKind.WHISKY, "Whisky"],
  [SpiritKind.CANADIAN_WHISKY, "Canadian Whisky"],
  [SpiritKind.RYE_WHISKY, "Rye Whisky"],
  [SpiritKind.GIN, "Gin"],
  [SpiritKind.VODKA, "Vodka"],
  [SpiritKind.RUM, "Rum"],
  [SpiritKind.BRANDY, "Brandy"],
  [SpiritKind.LIQUEUR, "Liqueur"],
  [SpiritKind.OTHER, "Other"],
]);

export function spiritKindLabel(k: SpiritKind): string {
  return spiritKindLabels.get(k) ?? "Unknown";
}

const mashStatusLabels = new Map<MashStatus, string>([
  [MashStatus.PLANNED, "Planned"],
  [MashStatus.IN_PROGRESS, "In progress"],
  [MashStatus.FERMENTING, "Fermenting"],
  [MashStatus.DISTILLED, "Distilled"],
  [MashStatus.CANCELLED, "Cancelled"],
]);

export function mashStatusLabel(s: MashStatus): string {
  return mashStatusLabels.get(s) ?? "Unknown";
}

const mashMetricKindLabels = new Map<MashMetricKind, string>([
  [MashMetricKind.ORIGINAL_GRAVITY, "Original gravity"],
  [MashMetricKind.MASH_PH, "Mash pH"],
  [MashMetricKind.MASH_TEMP_C, "Mash temp (°C)"],
  [MashMetricKind.WATER_VOLUME_L, "Water volume (L)"],
  [MashMetricKind.STRIKE_TEMP_C, "Strike temp (°C)"],
  [MashMetricKind.OTHER, "Other"],
]);

export function mashMetricKindLabel(k: MashMetricKind): string {
  return mashMetricKindLabels.get(k) ?? "Unknown";
}

const fermentationStatusLabels = new Map<FermentationStatus, string>([
  [FermentationStatus.PITCHED, "Pitched"],
  [FermentationStatus.ACTIVE, "Active"],
  [FermentationStatus.FINISHED, "Finished"],
  [FermentationStatus.DISTILLED, "Distilled"],
  [FermentationStatus.CANCELLED, "Cancelled"],
]);

export function fermentationStatusLabel(s: FermentationStatus): string {
  return fermentationStatusLabels.get(s) ?? "Unknown";
}
