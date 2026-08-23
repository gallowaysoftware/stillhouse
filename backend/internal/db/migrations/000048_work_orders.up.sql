-- 000048_work_orders: what a second person needs.
--
-- Stillhouse recorded what had happened and never what was going to.
-- That is workable for an owner-operator who holds the plan in their
-- head, and it is the thing that stops working the day somebody else
-- walks in — because "what should I be doing" has no answer in the
-- system, and the answer that exists is in somebody's head or on a
-- whiteboard that the ledger knows nothing about.
--
-- A work order is deliberately thin. It is an intention with a subject,
-- an owner and a date — not a second copy of the production record. The
-- temptation is to make it hold quantities and then reconcile them
-- against what actually happened; that way lies two sources of truth for
-- the same batch, and the one people trust is whichever they typed into
-- last. So a work order points at the thing it produced and stops there.

CREATE TYPE work_order_kind AS ENUM (
    'mash', 'fermentation', 'distillation', 'bottling',
    'barrel_fill', 'barrel_dump', 'regauge', 'cleaning', 'maintenance', 'other'
);

CREATE TYPE work_order_status AS ENUM (
    'planned', 'in_progress', 'done', 'cancelled'
);

CREATE TABLE work_orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- Sequential per tenant, the way a person refers to it out loud.
    work_order_no  INTEGER NOT NULL,
    kind           work_order_kind NOT NULL,
    status         work_order_status NOT NULL DEFAULT 'planned',
    title          TEXT NOT NULL CHECK (length(trim(title)) > 0),
    detail         TEXT NOT NULL DEFAULT '',

    -- Who it is for. Nullable: an unassigned job on the board is a real
    -- state and the commonest one when a week is being planned.
    assigned_to    UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Or a role, when it is "somebody who can operate the still" rather
    -- than a named person.
    assigned_role  user_role,

    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    scheduled_for  DATE,
    due_on         DATE,

    -- What it is about, when it is about something that already exists —
    -- regauging a specific cask, bottling a specific product. At most
    -- one, which the CHECK enforces.
    container_id   UUID REFERENCES bulk_containers(id) ON DELETE CASCADE,
    product_id     UUID REFERENCES products(id)        ON DELETE CASCADE,
    recipe_id      UUID REFERENCES recipes(id)         ON DELETE CASCADE,

    -- What it produced. The link runs this way — order points at record —
    -- so the production tables know nothing about planning and a work
    -- order can be deleted without touching the ledger.
    mash_run_id         UUID REFERENCES mash_runs(id)         ON DELETE SET NULL,
    distillation_run_id UUID REFERENCES distillation_runs(id) ON DELETE SET NULL,
    bottling_run_id     UUID REFERENCES bottling_runs(id)     ON DELETE SET NULL,

    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    completed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    cancel_reason  TEXT NOT NULL DEFAULT '',

    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, work_order_no),
    CONSTRAINT work_orders_one_subject_chk CHECK (
        (container_id IS NOT NULL)::int
      + (product_id IS NOT NULL)::int
      + (recipe_id IS NOT NULL)::int <= 1
    ),
    CONSTRAINT work_orders_dates_chk CHECK (
        due_on IS NULL OR scheduled_for IS NULL OR due_on >= scheduled_for
    )
);

-- The board: what is open, soonest first. Partial, because a done work
-- order is history and history does not need to be fast.
CREATE INDEX work_orders_open_idx
    ON work_orders (tenant_id, scheduled_for, due_on)
    WHERE status IN ('planned', 'in_progress');

CREATE INDEX work_orders_assignee_idx
    ON work_orders (assigned_to, status)
    WHERE assigned_to IS NOT NULL;

CREATE TRIGGER work_orders_updated_at
    BEFORE UPDATE ON work_orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE work_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_orders FORCE  ROW LEVEL SECURITY;
CREATE POLICY work_orders_tenant ON work_orders FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- Two alert kinds the board makes possible: work that is late, and work
-- due today that nobody owns.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'work_order_overdue';
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'work_order_unassigned';

GRANT SELECT, INSERT, UPDATE, DELETE ON work_orders TO stillhouse_app;
