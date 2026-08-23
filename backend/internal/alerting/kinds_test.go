package alerting

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryEmittedKindCanBeResolved guards the one way this package can
// fail silently and badly.
//
// Kinds is what ResolveStaleAlerts is scoped to. An alert whose kind is
// missing from it opens normally, is emailed normally, and then never
// closes — because the sweep that resolves conditions that have stopped
// being true does not consider it. The dashboard fills with things that
// are no longer happening, which is precisely the failure stage 160 was
// built to avoid.
//
// It had already happened by the time this test was written. Three rules
// were added across stages 162, 171 and 172 and the list was not
// updated, so licence renewals, overdue work and open redistillations
// would all have stuck open forever. Nothing caught it until a test
// asserted that recording a redistillation's output cleared its alert.
//
// So: parse this package for every AlertKind constant used to build an
// Alert, and check each one is in the list. Same shape as the
// procedure-coverage test in internal/rpc, which has earned its keep
// twice for the same reason — a hand-maintained list that has to stay in
// step with code nobody thinks to look at.
func TestEveryEmittedKindCanBeResolved(t *testing.T) {
	emitted := emittedKinds(t)
	if len(emitted) < 5 {
		t.Fatalf("only found %d emitted alert kinds — the parser is looking in the wrong place",
			len(emitted))
	}

	resolvable := map[string]bool{}
	for _, k := range Kinds {
		resolvable[k] = true
	}

	var missing []string
	for _, k := range emitted {
		// Kinds holds the database's own strings; the constant name is
		// AlertKindFooBar for a value of foo_bar.
		if !resolvable[dbValueFor(k)] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d alert kind(s) are raised by a rule but missing from Kinds:\n  %s\n"+
			"ResolveStaleAlerts is scoped to Kinds, so these open and never close — "+
			"the dashboard fills with conditions that have stopped being true.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// dbValueFor turns AlertKindFilingDue into filing_due.
func dbValueFor(constName string) string {
	name := strings.TrimPrefix(constName, "AlertKind")
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// emittedKinds finds every sqlcgen.AlertKindX referenced as the Kind of
// an Alert literal in this package.
func emittedKinds(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "Alert" {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Kind" {
					continue
				}
				sel, ok := kv.Value.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "sqlcgen" {
					seen[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
