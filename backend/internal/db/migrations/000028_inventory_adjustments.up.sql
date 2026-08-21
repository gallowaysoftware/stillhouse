-- ------------------------------------------------------------------------
-- Inventory adjustments: reconciling book inventory to physical.
--
-- Line D on B266 page 3 (and line F on B262) is a real, reason-coded entry.
-- Stillhouse had no adjustment concept at all, and the gap showed up three
-- ways:
--
--   * RegaugeBarrel refuses any upward variance outright — "new LAA cannot
--     exceed current LAA, regauges record losses only" — so a cask that
--     gauges higher than the book (a mis-keyed fill, an instrument error, a
--     genuine count) had no path.
--   * Tanks could not be reconciled at all: regauge is barrel-only.
--   * A downward variance on a barrel was booked as loss_evaporation
--     whatever caused it, so a counting error and the angels' share landed
--     on the same line — and under EDM3-4-1 they do not have the same duty
--     treatment.
--
-- An adjustment is a determination like any other gauge, so it carries the
-- instruments that made it (stage 144) and goes through the same 20 °C
-- correction. What makes it different from a regauge is that it says WHY,
-- names WHO, and keeps what the book said beside what was counted.
-- ------------------------------------------------------------------------

-- Direction lives in the reason, matching transfer_in_bond /
-- transfer_out_in_bond and loss_evaporation / loss_unaccounted. The
-- alternative — one reason with the direction implied by which end of the
-- movement is set — would make SumBulkMovementsByReason add increases and
-- decreases together and report their sum as though it were a total.
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'adjustment_increase';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'adjustment_decrease';

-- Why the book and the physical stock disagreed. Not a free-text field:
-- line D is reason-coded, and "other" carries a mandatory explanation.
CREATE TYPE inventory_adjustment_reason AS ENUM (
    -- Stock was counted or gauged and differs from the ledger.
    'physical_count',
    -- An earlier determination was wrong: instrument error, arithmetic,
    -- a reading taken at the wrong temperature.
    'measurement_correction',
    -- A keying mistake in Stillhouse itself.
    'data_entry_error',
    'other'
);

CREATE TABLE inventory_adjustments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    container_id        UUID NOT NULL REFERENCES bulk_containers(id) ON DELETE RESTRICT,
    -- The ledger row this adjustment wrote. NULL only when the adjustment
    -- moved no alcohol — a gauge that confirmed the book exactly, which is
    -- worth recording precisely because it is evidence the count was done.
    bulk_movement_id    UUID REFERENCES bulk_movements(id),

    reason              inventory_adjustment_reason NOT NULL,
    -- Always required. A reconciliation entry is read by an auditor asking
    -- why the numbers moved, and the reason code alone does not answer it.
    explanation         TEXT NOT NULL CHECK (length(trim(explanation)) > 0),

    -- What the ledger said, immediately before the adjustment.
    book_volume_l       DOUBLE PRECISION NOT NULL,
    book_abv_pct        DOUBLE PRECISION,
    book_laa            DOUBLE PRECISION NOT NULL,
    -- What was actually found, corrected to 20 °C.
    counted_volume_l    DOUBLE PRECISION NOT NULL CHECK (counted_volume_l >= 0),
    counted_abv_pct     DOUBLE PRECISION,
    counted_laa         DOUBLE PRECISION NOT NULL CHECK (counted_laa >= 0),
    -- Signed: negative when the count found less than the book. The one
    -- figure line D is built from.
    delta_laa           DOUBLE PRECISION NOT NULL,
    delta_volume_l      DOUBLE PRECISION NOT NULL,

    -- The determination trail, same as a gauge (stage 144, migration 000023).
    temperature_c            DOUBLE PRECISION,
    observed_volume_l        DOUBLE PRECISION,
    observed_density_kg_m3   DOUBLE PRECISION,
    volume_factor_c          DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    strength_source          strength_source NOT NULL DEFAULT 'uncorrected',
    volume_instrument_id      UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    strength_instrument_id    UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    temperature_instrument_id UUID REFERENCES instruments(id) ON DELETE RESTRICT,

    -- Who. An adjustment is an attributable act, not a correction that
    -- appears by itself.
    adjusted_by         UUID NOT NULL REFERENCES users(id),
    notes               TEXT NOT NULL DEFAULT '',
    occurred_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX inventory_adjustments_tenant_occurred_idx
    ON inventory_adjustments (tenant_id, occurred_at DESC);
CREATE INDEX inventory_adjustments_container_idx
    ON inventory_adjustments (container_id, occurred_at DESC);

ALTER TABLE inventory_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_adjustments FORCE  ROW LEVEL SECURITY;
CREATE POLICY inventory_adjustments_tenant ON inventory_adjustments FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
