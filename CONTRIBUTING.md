# Contributing to Stillhouse

Thanks for looking. A few things worth knowing before you open a PR.

## What this project is

Stillhouse is production software for CRA-licensed Canadian spirits
producers. Numbers it prints end up on a Form B266 that a real distillery
files with the Canada Revenue Agency. That shapes how changes are
reviewed.

## The rules that matter here

**Don't invent domain constants.** If a calculation needs a rate, a
correction table, or a regulatory figure, cite the source. If you can't
find an authoritative one, say so in the PR rather than approximating —
a plausible-looking wrong number in a duty calculation is worse than a
missing feature. Constants that come from a published source carry the
citation on the constant itself; follow that pattern.

Where a source is silent, the code says so rather than interpolating.
`internal/mashing` reports "no published gelatinisation range" for oats
instead of guessing one, and that is the house style.

**Alcohol is conserved.** Any change that moves alcohol between vessels
must neither create nor destroy litres of absolute alcohol. There are
tests asserting this; keep them passing, and add one if you introduce a
new path.

**Strength is a 20 °C quantity.** Never store or compare a strength
without knowing the temperature it was measured at.

**Canada only.** LAA, not proof gallons. B266, not 5110.40. Bulk and
packaged, not in-bond and tax-determined. Don't reference TTB, DSP or the
5110-series anywhere in shipped code, docs or UI.

## Before you push

```sh
make lint     # gofmt + go vet + golangci-lint + buf lint + tsc
make test
```

CI runs the same checks plus an integration suite against a real
Postgres. Generated code (`internal/genpb/`, `web/src/gen/`,
`internal/db/sqlcgen/`) is committed — regenerate it with `make generate`
after touching a `.proto` or a `.sql`, or CI will fail on drift.

## Commit style

One commit per shipped change, titled `stage N: <terse one-liner>`, with
a row added to the README stages table in the same commit. The body
should explain *why*, not restate the diff — including anything you
considered and rejected, which is often the most useful part later.

## Licensing and contributions — please read

Stillhouse is licensed under the **AGPL-3.0**. Contributions are accepted
under the same licence.

Be aware that the maintainer may in future offer Stillhouse under a
separate commercial licence alongside the AGPL. Doing that requires
holding sufficient rights in all contributed code. **A contributor
licence agreement is not yet in place.** Until one is, please assume that
substantial outside contributions may be declined, or may need to be
re-implemented, rather than merged into a codebase that could later be
dual-licensed.

If you want to contribute something substantial, open an issue first and
we'll sort the licensing out before you spend the time.

## Reporting problems

Bugs and questions: GitHub issues. Security: see SECURITY.md — please
don't open a public issue for those.
