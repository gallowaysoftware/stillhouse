-- Whose bottles these are.
--
-- Stage 176 gave bulk containers an owner and left packaged stock
-- without one, so the chain from a removal back to whoever owned the
-- spirits stopped at the bottling run. Cost of sales valued every
-- removal as if the goods were the licensee's, and the journal attached
-- a warning saying so — a known-wrong figure with a note on it, which is
-- better than a silent one and worse than a right one.
--
-- The item was raised assuming ownership had to be effective-dated,
-- because a cask sold in place last quarter would otherwise restate a
-- closed period. It does not: the owner is copied onto the lot at the
-- moment it is packaged, the way bill_to_name is copied onto an invoice
-- at issue and destination_name onto a removal at the event. A document
-- already produced does not change when the underlying record does, and
-- a lot packaged from a customer's cask is theirs whatever happens to
-- the cask afterwards.

ALTER TABLE packaged_inventory
    ADD COLUMN owner_customer_id UUID REFERENCES customers(id) ON DELETE RESTRICT;

ALTER TABLE bottling_runs
    ADD COLUMN owner_customer_id UUID REFERENCES customers(id) ON DELETE RESTRICT;

ALTER TABLE marked_special_containers
    ADD COLUMN owner_customer_id UUID REFERENCES customers(id) ON DELETE RESTRICT;

CREATE INDEX packaged_inventory_owner_idx ON packaged_inventory (owner_customer_id)
    WHERE owner_customer_id IS NOT NULL;
CREATE INDEX bottling_runs_owner_idx ON bottling_runs (owner_customer_id)
    WHERE owner_customer_id IS NOT NULL;

-- Nothing is backfilled. Every lot that exists today was packaged before
-- Stillhouse could express third-party ownership, which means it was the
-- licensee's — NULL says exactly that, and inventing an owner for a
-- historical lot would be worse than leaving it as it was.
