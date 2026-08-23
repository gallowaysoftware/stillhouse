package rpc

import (
	"context"
	"fmt"
	"sort"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// lockContainers takes a row lock on every container a transaction is about
// to change, and returns them by id.
//
// Balances are stored, not derived, and every write computes an absolute
// new value from a previous read. Without a lock two transactions read the
// same volume, both compute a new one, and the second commit throws the
// first one's withdrawal away — which for an excise ledger means alcohol
// appearing out of nowhere. Eight concurrent barrel fills from one tank
// moved 800 L while the tank fell by 100.
//
// Ids are sorted before locking. Two transactions touching the same pair of
// containers in opposite orders would otherwise each hold what the other
// needs; a blend across several tanks makes that ordinary rather than
// exotic. Sorting gives every caller the same acquisition order, so they
// queue instead of deadlocking.
func lockContainers(
	ctx context.Context,
	q *sqlcgen.Queries,
	ids ...uuid.UUID,
) (map[uuid.UUID]sqlcgen.BulkContainer, error) {
	ordered := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].String() < ordered[j].String()
	})

	out := make(map[uuid.UUID]sqlcgen.BulkContainer, len(ordered))
	for _, id := range ordered {
		c, err := q.GetBulkContainerForUpdate(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := assertHeld(c); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, nil
}

// lockContainerForWrite is lockContainers for the callers that only ever
// touch one container, so the possession check below is not something a
// path can be written without.
func lockContainerForWrite(
	ctx context.Context,
	q *sqlcgen.Queries,
	id uuid.UUID,
) (sqlcgen.BulkContainer, error) {
	c, err := q.GetBulkContainerForUpdate(ctx, id)
	if err != nil {
		return c, err
	}
	return c, assertHeld(c)
}

// assertHeld refuses to change the balance of spirits we do not hold.
//
// This is the second half of what makes the B266 bulk walk correct across
// a change of possession — see rpc.SetBulkContainerPossession and the
// comment on SumBulkOnHandAsOf. It is also just true: you cannot gauge a
// cask in somebody else's warehouse, and a fill, a draw or a loss
// recorded against one is a figure nobody measured. Whatever happened to
// the spirits while they were away is the holder's record, reconciled by
// a regauge when they come back.
//
// SetBulkContainerPossession deliberately does not go through here — it is
// the one write an off-premises container must accept.
func assertHeld(c sqlcgen.BulkContainer) error {
	if c.Possession != sqlcgen.BulkPossessionHeldElsewhere {
		return nil
	}
	holder := c.HeldByName
	if holder == "" {
		holder = "another licensee"
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"%s is held by %s — record it back into your possession before "+
			"recording anything against it", c.Name, holder))
}
