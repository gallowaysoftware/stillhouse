-- ------------------------------------------------------------------------
-- Filing frequency, fiscal months, and the due date.
--
-- Stillhouse assumed a calendar month and derived nothing from it. Two
-- things are wrong with that under EDM3-1-1 para 50:
--
--   * A fiscal month is set by notification (form B268), not assumed. A
--     licensee may elect fiscal months that end on a fixed day, or four-
--     and five-week periods, and CRA files against what they elected.
--   * An authorized licensee may file SEMI-ANNUALLY (form B284) rather
--     than monthly, which changes both the period length and the due date.
--
-- And the due date itself did not exist anywhere: the return is due by the
-- last day of the fiscal month following the reporting period, and nothing
-- in the model could say when that was. A filing calendar nobody can
-- compute is the reason H5's notifications have nothing to fire on.
-- ------------------------------------------------------------------------

CREATE TYPE filing_frequency AS ENUM ('monthly', 'semi_annual');

-- How fiscal months are defined for this licensee.
--   calendar_month    — the default, and what every tenant has been on.
--   fixed_day_of_month — fiscal months end on a nominated day (the 25th,
--                        say), which is the common election for a
--                        distillery whose accounting month does not end on
--                        the 31st.
CREATE TYPE fiscal_month_basis AS ENUM ('calendar_month', 'fixed_day_of_month');

ALTER TABLE tenants
    ADD COLUMN filing_frequency   filing_frequency NOT NULL DEFAULT 'monthly',
    ADD COLUMN fiscal_month_basis fiscal_month_basis NOT NULL DEFAULT 'calendar_month',
    -- The day a fiscal month ends, when the basis is fixed_day_of_month.
    -- Capped at 28 rather than 31: a fiscal month ending on the 30th has
    -- no February, and the elections CRA accepts do not create one.
    ADD COLUMN fiscal_month_end_day INTEGER
        CHECK (fiscal_month_end_day IS NULL
               OR (fiscal_month_end_day BETWEEN 1 AND 28)),
    -- The B268 notification and the B284 authorization behind the two
    -- above. Neither election is something a licensee just decides — each
    -- is filed with CRA — so the reference belongs beside the setting.
    ADD COLUMN fiscal_month_notification_ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN filing_frequency_authorization_ref TEXT NOT NULL DEFAULT '';

-- A fixed-day basis without a day is a setting that cannot be applied.
ALTER TABLE tenants
    ADD CONSTRAINT tenants_fiscal_basis_needs_day
    CHECK (fiscal_month_basis <> 'fixed_day_of_month'
           OR fiscal_month_end_day IS NOT NULL);

-- The due date is derived, but it is stored on the period once generated
-- so that a filed return keeps the date it was actually due — the same
-- reasoning as the snapshot. Changing the fiscal-month election later must
-- not silently restate when a past return was due.
ALTER TABLE b266_periods
    ADD COLUMN due_on DATE;

COMMENT ON COLUMN b266_periods.due_on IS
    'Last day of the fiscal month following the reporting period (EDM3-1-1 para 50). Frozen at generation so a later change of election does not restate when a past return was due.';
