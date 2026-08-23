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
  thirdPartyElsewhereLaa: number;
  thirdPartyCount: number;
};

// The header line that stops one number being asked two questions.
// Silent until ownership and possession actually diverge, so an ordinary
// distillery never sees a distinction it does not have.
export function OwnershipSplit({ s, noun }: { s: Split; noun: string }) {
  // Three clauses, so between them they account for everything that is
  // not simply yours and here. Two of them left a customer's cask that
  // had also gone elsewhere unmentioned, and the sentence read as though
  // nothing were unaccounted for.
  const parts: string[] = [];
  if (s.heldForOthersLaa > 0) {
    parts.push(
      `${formatLAA(s.heldForOthersLaa)} L is held for customers — on your B266, not your books`,
    );
  }
  if (s.heldElsewhereLaa > 0) {
    parts.push(
      `${formatLAA(s.heldElsewhereLaa)} L of your own ${noun} sits with another licensee — on your books, not your return`,
    );
  }
  if (s.thirdPartyElsewhereLaa > 0) {
    parts.push(
      `${formatLAA(s.thirdPartyElsewhereLaa)} L is a customer's and also elsewhere — on neither`,
    );
  }
  if (parts.length === 0) return null;
  return <span className="text-fg-muted"> Of that, {parts.join("; ")}.</span>;
}
