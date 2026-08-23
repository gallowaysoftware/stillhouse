import { BulkPossession } from "@/gen/stillhouse/v1/bulk_pb";
import { formatLAA } from "@/lib/format";

type Owned = {
  ownerName?: string;
  ownerCustomerId?: string;
  possession?: BulkPossession;
  heldByName?: string;
};

// Two facts, and they answer different questions. Ownership decides
// whether the alcohol is an asset; possession decides whether it goes on
// the B266. A container that is simply ours and here says nothing, so the
// badge only appears when there is something to say.
export function OwnershipBadge({ c }: { c: Owned }) {
  const elsewhere = c.possession === BulkPossession.HELD_ELSEWHERE;
  const theirs = Boolean(c.ownerCustomerId);
  if (!elsewhere && !theirs) return null;
  return (
    <span className="ml-2 inline-flex gap-1 align-middle">
      {theirs && (
        <span
          title="Owned by a customer. On your B266 while you hold it; not your inventory to value or sell."
          className="rounded bg-surface-3 px-1.5 py-0.5 text-[11px] font-medium text-fg-muted"
        >
          {c.ownerName || "customer-owned"}
        </span>
      )}
      {elsewhere && (
        <span
          title="Held by another licensee. On your books; not on your B266 — they report it."
          className="rounded bg-warning/15 px-1.5 py-0.5 text-[11px] font-medium text-warning-fg"
        >
          at {c.heldByName || "another licensee"}
        </span>
      )}
    </span>
  );
}

type Split = {
  totalLaa: number;
  ownedLaa: number;
  heldLaa: number;
  availableLaa?: number;
  heldForOthersLaa: number;
  heldElsewhereLaa: number;
  thirdPartyCount: number;
};

// The header line that stops one number being asked two questions.
// Silent until ownership and possession actually diverge, so an ordinary
// distillery never sees a distinction it does not have.
export function OwnershipSplit({ s, noun }: { s: Split; noun: string }) {
  if (s.heldForOthersLaa === 0 && s.heldElsewhereLaa === 0) return null;
  return (
    <span className="text-fg-muted">
      {" "}Of that, <span className="font-medium text-fg">{formatLAA(s.heldForOthersLaa)} L</span>{" "}
      is held for customers — on your B266, not your books — and{" "}
      <span className="font-medium text-fg">{formatLAA(s.heldElsewhereLaa)} L</span> of your own{" "}
      {noun} sits with another licensee, on your books but on their return.
    </span>
  );
}
