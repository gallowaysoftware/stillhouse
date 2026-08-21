-- ------------------------------------------------------------------------
-- Duty point: the event at which excise duty becomes payable.
--
-- Stillhouse computed duty in exactly one place — a removal — which is the
-- excise-warehouse pattern: hold packaged spirits non-duty-paid, pay when
-- they leave for the duty-paid market.
--
-- A spirits licensee WITHOUT an excise warehouse licence cannot possess
-- non-duty-paid packaged spirits at all (EDM3-1-1 para 18). For that
-- licensee duty becomes payable at the time the spirits are PACKAGED
-- (para 29). Stillhouse was reporting duty in the month of sale when CRA
-- expects it in the month of bottling — a timing error on a filed return,
-- carrying interest.
--
-- The duty point is derived, not toggled: it follows from whether the
-- tenant holds an excise warehouse licence. A generated column makes it
-- impossible for the two to disagree. When the licence register (PLAN B1)
-- replaces the free-text licence field, this expression moves with it.
-- ------------------------------------------------------------------------

CREATE TYPE duty_point AS ENUM ('at_packaging', 'at_removal');

ALTER TABLE tenants
    ADD COLUMN duty_point duty_point NOT NULL
        GENERATED ALWAYS AS (
            CASE
                WHEN excise_warehouse_licence_number IS NOT NULL
                 AND excise_warehouse_licence_number <> ''
                THEN 'at_removal'::duty_point
                ELSE 'at_packaging'::duty_point
            END
        ) STORED;

-- duty_point_effective_from is the cutover: the first day the derived duty
-- point governs. Before it, duty crystallised at removal, because that is
-- what Stillhouse did and what has already been filed.
--
-- Nothing already filed moves. Submitted B266 periods keep their frozen
-- snapshots, and a removal of stock packaged before the cutover still
-- carries duty even for an at-packaging tenant — that stock was never
-- dutied at its bottling, so if the removal stopped carrying duty the
-- litres would be dutied never. Stock packaged on or after the cutover is
-- dutied at packaging and its removal carries none, so no litre is dutied
-- twice.
--
-- Existing tenants: the cutover is the day this migration runs. Everything
-- before it keeps the basis it was recorded on.
-- New tenants: the column defaults to the day the tenant is created, so
-- there is no pre-cutover history to grandfather.
ALTER TABLE tenants
    ADD COLUMN duty_point_effective_from DATE NOT NULL DEFAULT CURRENT_DATE;

COMMENT ON COLUMN tenants.duty_point IS
    'Derived from excise_warehouse_licence_number: a licensee without a warehouse licence pays at packaging (EDM3-1-1 para 29), one with a warehouse licence pays at removal.';
COMMENT ON COLUMN tenants.duty_point_effective_from IS
    'First day duty_point governs. Duty events before this date used the at-removal basis, which is what has already been filed.';

-- ------------------------------------------------------------------------
-- Duty on the bottling run.
--
-- The event that makes duty payable for an at-packaging licensee, recorded
-- where it happens rather than derived at report time. duty_rate_per_laa
-- is zero for the at-or-under-7% band, which is charged per litre of
-- product and not per litre of absolute alcohol — the same convention the
-- removal row uses, so a caller that multiplies a low-strength quantity by
-- a per-LAA rate gets a visible zero rather than a plausible number.
--
-- Nullable, not defaulted to zero: NULL means "no duty event here" (an
-- at-removal tenant, or a run before the cutover) and is different from
-- "dutied at zero". Runs that predate this migration are NULL, and their
-- stock is dutied on removal as it always was.
-- ------------------------------------------------------------------------
ALTER TABLE bottling_runs
    ADD COLUMN duty_rate_per_laa DOUBLE PRECISION,
    ADD COLUMN duty_amount_cad   DOUBLE PRECISION
        CHECK (duty_amount_cad IS NULL OR duty_amount_cad >= 0),
    -- The CRA notice the rate was read from, so the figure on a filed
    -- return can be checked against its source years later.
    ADD COLUMN duty_rate_source  TEXT NOT NULL DEFAULT '';

CREATE INDEX bottling_runs_duty_idx
    ON bottling_runs (tenant_id, bottling_date)
    WHERE duty_amount_cad IS NOT NULL AND voided_at IS NULL;
