import {
  BulkContainerKind,
  BulkMovementReason,
} from "@/gen/stillhouse/v1/bulk_pb";
import {
  DistillationCutKind,
  DistillationStatus,
} from "@/gen/stillhouse/v1/distillation_pb";
import { FermentationStatus } from "@/gen/stillhouse/v1/fermentation_pb";
import { MashMetricKind, MashStatus } from "@/gen/stillhouse/v1/mash_pb";
import { Cereal, MaterialKind } from "@/gen/stillhouse/v1/material_pb";
import { BotanicalRole, DistillationMethod, SpiritKind } from "@/gen/stillhouse/v1/recipe_pb";

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

// Money is not a quantity. formatQty renders 2–4 decimals, which is right
// for litres and wrong for dollars — duty payable on a B266 was rendering
// as $1,234.5678. CAD always gets exactly two.
const cadFormatter = new Intl.NumberFormat("en-CA", {
  style: "currency",
  currency: "CAD",
  currencyDisplay: "narrowSymbol",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

export function formatCAD(v: number | undefined): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return cadFormatter.format(v);
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

const botanicalRoleLabels = new Map<BotanicalRole, string>([
  [BotanicalRole.UNSPECIFIED, "—"],
  [BotanicalRole.JUNIPER, "Juniper"],
  [BotanicalRole.CITRUS, "Citrus"],
  [BotanicalRole.HERBAL, "Herbal"],
  [BotanicalRole.SPICE, "Spice"],
  [BotanicalRole.FLORAL, "Floral"],
  [BotanicalRole.ROOT, "Root"],
  [BotanicalRole.OTHER, "Other"],
]);

export function botanicalRoleLabel(r: BotanicalRole): string {
  return botanicalRoleLabels.get(r) ?? "—";
}

export const BOTANICAL_ROLE_OPTIONS: Array<{ value: BotanicalRole; label: string }> = [
  { value: BotanicalRole.UNSPECIFIED, label: "— none —" },
  { value: BotanicalRole.JUNIPER, label: "Juniper" },
  { value: BotanicalRole.CITRUS, label: "Citrus" },
  { value: BotanicalRole.HERBAL, label: "Herbal" },
  { value: BotanicalRole.SPICE, label: "Spice" },
  { value: BotanicalRole.FLORAL, label: "Floral" },
  { value: BotanicalRole.ROOT, label: "Root" },
  { value: BotanicalRole.OTHER, label: "Other" },
];

const distillationMethodLabels = new Map<DistillationMethod, string>([
  [DistillationMethod.UNSPECIFIED, "—"],
  [DistillationMethod.POT, "Pot (full maceration)"],
  [DistillationMethod.VAPOR, "Vapor infusion (gin basket)"],
  [DistillationMethod.COMBINED, "Combined pot + vapor"],
]);

export function distillationMethodLabel(m: DistillationMethod): string {
  return distillationMethodLabels.get(m) ?? "—";
}

export const DISTILLATION_METHOD_OPTIONS: Array<{ value: DistillationMethod; label: string }> = [
  { value: DistillationMethod.UNSPECIFIED, label: "— not set —" },
  { value: DistillationMethod.POT, label: "Pot (full maceration in still)" },
  { value: DistillationMethod.VAPOR, label: "Vapor (gin basket)" },
  { value: DistillationMethod.COMBINED, label: "Combined pot + vapor" },
];

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
  [MashMetricKind.WASH_VOLUME_L, "Wash volume (L)"],
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

const bulkContainerKindLabels = new Map<BulkContainerKind, string>([
  [BulkContainerKind.SPIRIT_RECEIVER, "Spirit receiver"],
  [BulkContainerKind.TANK, "Tank"],
  [BulkContainerKind.IBC, "IBC"],
  [BulkContainerKind.TOTE, "Tote"],
  [BulkContainerKind.BLEND_TANK, "Blend tank"],
  [BulkContainerKind.BOTTLING_TANK, "Bottling tank"],
  [BulkContainerKind.OTHER, "Other"],
]);

export function bulkContainerKindLabel(k: BulkContainerKind): string {
  return bulkContainerKindLabels.get(k) ?? "Unknown";
}

const bulkMovementReasonLabels = new Map<BulkMovementReason, string>([
  [BulkMovementReason.PRODUCTION_GAUGE, "Production gauge"],
  [BulkMovementReason.INTER_TANK_TRANSFER, "Inter-tank transfer"],
  [BulkMovementReason.BLEND, "Blend"],
  [BulkMovementReason.TRANSFER_IN_BOND, "Transfer in bond"],
  [BulkMovementReason.TRANSFER_OUT_IN_BOND, "Transfer out (in bond)"],
  [BulkMovementReason.TRANSFER_TO_PACKAGING, "Transfer to packaging"],
  [BulkMovementReason.LOSS_EVAPORATION, "Loss (evaporation)"],
  [BulkMovementReason.LOSS_UNACCOUNTED, "Loss (unaccounted)"],
  [BulkMovementReason.REGAUGE_CORRECTION, "Regauge correction"],
  [BulkMovementReason.DESTRUCTION, "Destruction"],
  [BulkMovementReason.OPENING_INVENTORY, "Opening inventory (adopted)"],
  [BulkMovementReason.ADJUSTMENT_INCREASE, "Adjustment (increase)"],
  [BulkMovementReason.ADJUSTMENT_DECREASE, "Adjustment (decrease)"],
  [BulkMovementReason.IMPORT_RECEIVED, "Imported bulk spirits"],
  [BulkMovementReason.RECEIVED_FROM_SPIRITS_LICENSEE, "Received from spirits licensee"],
  [BulkMovementReason.RECEIVED_FROM_LICENSED_USER, "Received from licensed user"],
  [BulkMovementReason.PACKAGED_RETURNED_TO_BULK, "Packaged returned to bulk"],
  [BulkMovementReason.DELIVERED_TO_SPIRITS_LICENSEE, "Delivered to spirits licensee"],
  [BulkMovementReason.DELIVERED_TO_LICENSED_USER, "Delivered to licensed user"],
  [BulkMovementReason.EXPORTED, "Exported"],
  [BulkMovementReason.DENATURED_DA, "Denatured to DA"],
  [BulkMovementReason.DENATURED_SDA, "Denatured to SDA"],
  [BulkMovementReason.RETURNED_TO_PRODUCTION, "Returned to production"],
]);

export function bulkMovementReasonLabel(r: BulkMovementReason): string {
  return bulkMovementReasonLabels.get(r) ?? "Unknown";
}

const distillationStatusLabels = new Map<DistillationStatus, string>([
  [DistillationStatus.PLANNED, "Planned"],
  [DistillationStatus.CHARGING, "Charging"],
  [DistillationStatus.DISTILLING, "Distilling"],
  [DistillationStatus.GAUGED, "Gauged"],
  [DistillationStatus.CANCELLED, "Cancelled"],
]);

export function distillationStatusLabel(s: DistillationStatus): string {
  return distillationStatusLabels.get(s) ?? "Unknown";
}

const cutKindLabels = new Map<DistillationCutKind, string>([
  [DistillationCutKind.FORESHOTS, "Foreshots"],
  [DistillationCutKind.HEADS, "Heads"],
  [DistillationCutKind.HEARTS, "Hearts"],
  [DistillationCutKind.TAILS, "Tails"],
  [DistillationCutKind.FEINTS_SAVED, "Feints (saved)"],
]);

export function cutKindLabel(k: DistillationCutKind): string {
  return cutKindLabels.get(k) ?? "Unknown";
}

// Cereal — grain species. Gelatinisation temperature is a property of the
// starch granule, so this (not malted/unmalted) is what drives the mash
// bench's temperature guidance. The hints carry the published range so the
// operator can see why the choice matters while making it.
const cerealLabels = new Map<Cereal, string>([
  [Cereal.UNSPECIFIED, "—"],
  [Cereal.BARLEY, "Barley"],
  [Cereal.WHEAT, "Wheat"],
  [Cereal.RYE, "Rye"],
  [Cereal.MAIZE, "Maize / corn"],
  [Cereal.RICE, "Rice"],
  [Cereal.OAT, "Oat"],
  [Cereal.OTHER, "Other"],
]);

export function cerealLabel(c: Cereal): string {
  return cerealLabels.get(c) ?? "—";
}

export const CEREAL_OPTIONS: Array<{ value: Cereal; label: string; hint?: string }> = [
  { value: Cereal.UNSPECIFIED, label: "— not set —", hint: "no temperature guidance" },
  { value: Cereal.BARLEY, label: "Barley", hint: "gelatinises 61–62 °C" },
  { value: Cereal.WHEAT, label: "Wheat", hint: "gelatinises 52–65 °C" },
  { value: Cereal.RYE, label: "Rye", hint: "gelatinises 60–65 °C" },
  { value: Cereal.MAIZE, label: "Maize / corn", hint: "70–80 °C — needs a cereal cook" },
  { value: Cereal.RICE, label: "Rice", hint: "70–80 °C — needs a cereal cook" },
  { value: Cereal.OAT, label: "Oat", hint: "no published range" },
  { value: Cereal.OTHER, label: "Other", hint: "no published range" },
];
