/** The calendar day a moment falls on where the operator is, as YYYY-MM-DD. */
export function isoDay(d: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function monthBounds(d: Date = new Date()): { start: string; end: string } {
  return {
    start: isoDay(new Date(d.getFullYear(), d.getMonth(), 1)),
    end: isoDay(new Date(d.getFullYear(), d.getMonth() + 1, 0)),
  };
}
