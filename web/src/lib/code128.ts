// Code 128, written out.
//
// A barcode library is a dependency for something that is a lookup table
// and two loops, on a page that must work with no CDN and no network. The
// encoding is fully specified (ISO/IEC 15417) and, more to the point, it
// is checkable: every symbol in the table has a shape the standard fixes,
// so a transcription error is caught by a test rather than by an operator
// waving a scanner at a label that will not read.
//
// Set B throughout. Set C packs digit pairs into one symbol and would
// make a numeric code shorter, but Stillhouse label codes are
// alphanumeric (see backend/internal/labelcode) and the saving does not
// arise. One encoding path is worth more here than a few millimetres.

// PATTERNS[v] is the bar/space module widths for symbol value v, starting
// with a bar and alternating. Every entry is six runs totalling 11
// modules; the stop symbol is seven runs totalling 13.
//
// The table's own invariants — six digits, widths 1..4, sum 11, an even
// number of bar modules, all distinct — are asserted in code128.test.ts.
// Together they catch any single-digit slip.
export const PATTERNS: readonly string[] = [
  "212222", "222122", "222221", "121223", "121322", "131222", "122213",
  "122312", "132212", "221213", "221312", "231212", "112232", "122132",
  "122231", "113222", "123122", "123221", "223211", "221132", "221231",
  "213212", "223112", "312131", "311222", "321122", "321221", "312212",
  "322112", "322211", "212123", "212321", "232121", "111323", "131123",
  "131321", "112313", "132113", "132311", "211313", "231113", "231311",
  "112133", "112331", "132131", "113123", "113321", "133121", "313121",
  "211331", "231131", "213113", "213311", "213131", "311123", "311321",
  "331121", "312113", "312311", "332111", "314111", "221411", "431111",
  "111224", "111422", "121124", "121421", "141122", "141221", "112214",
  "112412", "122114", "122411", "142112", "142211", "241211", "221114",
  "413111", "241112", "134111", "111242", "121142", "121241", "114212",
  "124112", "124211", "411212", "421112", "421211", "212141", "214121",
  "412121", "111143", "111341", "131141", "114113", "114311", "411113",
  "411311", "113141", "114131", "311141", "411131", "211412", "211214",
  "211232", "2331112",
];

export const START_B = 104;
export const STOP = 106;

/** Modules for one symbol value, as alternating bar/space run lengths. */
function runs(value: number): number[] {
  const p = PATTERNS[value];
  if (p === undefined) throw new Error(`Code 128: no pattern for value ${value}`);
  return [...p].map((c) => Number(c));
}

/**
 * Symbol values for `text` in Set B, start and check and stop included.
 *
 * Set B covers ASCII 32..127. Anything outside it cannot be encoded, and
 * throwing is the right answer: a barcode that silently drops a character
 * scans to something that is not what is printed under it.
 */
export function symbols(text: string): number[] {
  const out: number[] = [START_B];
  for (const ch of text) {
    const c = ch.codePointAt(0)!;
    if (c < 32 || c > 127) {
      throw new Error(`Code 128 set B cannot encode ${JSON.stringify(ch)}`);
    }
    out.push(c - 32);
  }
  // Weighted modulo-103 check. The start value counts once; each data
  // symbol counts by its one-based position.
  let sum = START_B;
  for (let i = 1; i < out.length; i++) sum += out[i] * i;
  out.push(sum % 103);
  out.push(STOP);
  return out;
}

/**
 * Module widths for `text`, alternating bar, space, bar, … starting with
 * a bar and ending on one.
 *
 * The stop symbol is seven runs, not six, and the seventh is the
 * two-module terminating bar — it is already in the pattern. Appending
 * another one is the obvious mistake here: it butts a bar against a bar,
 * which renders as one wider bar and decodes as neither.
 */
export function modules(text: string): number[] {
  const out: number[] = [];
  for (const v of symbols(text)) out.push(...runs(v));
  return out;
}

/** Total module count, which is what a width is measured in. */
export function moduleCount(text: string): number {
  return modules(text).reduce((a, b) => a + b, 0);
}

/**
 * An `<svg>` element's worth of markup for `text`, sized in modules so the
 * caller can scale it with CSS.
 *
 * Rendered as one path of filled rectangles rather than one rect per bar:
 * a sheet of forty cask tags is forty barcodes, and a few thousand DOM
 * nodes is the difference between a print dialog that opens and one that
 * hangs.
 */
export function svgPath(text: string): { d: string; width: number } {
  const runs = modules(text);
  let x = 0;
  let d = "";
  for (let i = 0; i < runs.length; i++) {
    const w = runs[i];
    if (i % 2 === 0) d += `M${x} 0h${w}v1h-${w}z`; // bar
    x += w;
  }
  return { d, width: x };
}
