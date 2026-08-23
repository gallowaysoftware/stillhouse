-- journal_event_kind keeps 'wip_to_finished_goods'.
-- Postgres cannot remove an enum value without rewriting the type and
-- every column using it, and a value nothing emits costs nothing. See
-- 000010's down half for why a destructive down migration is worse than
-- an untidy one.
DROP TABLE IF EXISTS labour_entries;
DROP TABLE IF EXISTS cost_rates;
DROP TYPE IF EXISTS overhead_basis;
