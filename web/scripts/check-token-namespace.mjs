// Guards the CSS custom-property namespace the colour tokens live in.
//
// These variables hold raw space-separated RGB channels ("9 9 11") rather
// than colours, because Tailwind 3 composes them as
// rgb(var(--sh-fg) / <alpha-value>) to get opacity utilities.
//
// Tailwind 4 claims --color-* for its own theme. A raw channel triple
// living there is not a colour, so the two meanings collide: the official
// codemod produces a build that exits 0 and an app that renders
// unstyled — a green build proving nothing, which is why PLAN H13 says
// this needs a human at a screen.
//
// It does not, if the collision simply never exists. This check is what
// keeps it from coming back the next time somebody adds a token by
// copying the line above it.
import { readFileSync } from "node:fs";

const files = ["src/index.css", "tailwind.config.js"];
const reserved = /--color-[a-z0-9-]+/g;

const bad = [];
for (const f of files) {
  const text = readFileSync(f, "utf8");
  for (const m of text.matchAll(reserved)) {
    bad.push(`${f}: ${m[0]}`);
  }
}

if (bad.length) {
  console.error(
    "Colour tokens must not live in the --color-* namespace: Tailwind 4 claims it,\n" +
      "and a raw channel triple there makes the app render unstyled on a build that\n" +
      "exits 0. Use the --sh-* prefix instead. Found:\n  " +
      bad.join("\n  "),
  );
  process.exit(1);
}

// And every token the config references must actually be defined, or it
// resolves to nothing and that element renders unstyled on its own.
const css = readFileSync("src/index.css", "utf8");
const cfg = readFileSync("tailwind.config.js", "utf8");
const defined = new Set([...css.matchAll(/(--sh-[a-z0-9-]+)\s*:/g)].map((m) => m[1]));
const used = new Set([...cfg.matchAll(/var\((--sh-[a-z0-9-]+)\)/g)].map((m) => m[1]));

const undef = [...used].filter((v) => !defined.has(v));
if (undef.length) {
  console.error("Tokens referenced by tailwind.config.js but never defined: " + undef.join(", "));
  process.exit(1);
}

console.log(`token namespace ok — ${defined.size} defined, ${used.size} referenced`);
