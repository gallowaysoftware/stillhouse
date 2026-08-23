package rpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryProcedureIsClassified is the guard against a whole class of
// silent failure: an RPC that is neither public nor classified in
// procedureMinRole falls through to the fail-closed default of roleOwner.
// That is the right direction to fail, but it fails QUIETLY — the endpoint
// simply stops working for everyone except owners, and nobody finds out
// until an operator reports that a button does nothing.
//
// It had already happened twice by the time this test was written:
//
//   - AlcoholometryService (ResolveStrength, TablesInfo, PlanReduction,
//     PlanBlend) was unclassified, so the strength-correction widget every
//     operator uses to land a gauge at 20 °C was owner-only.
//   - RequestPasswordReset and ResetPassword were in neither list, so the
//     password-reset flow demanded you be logged in to use it.
//
// Both are the kind of bug that a behavioural test can't find unless
// somebody thinks to write it for that specific endpoint. Enumerating the
// generated procedure constants finds all of them at once, including the
// next one somebody forgets.
func TestEveryProcedureIsClassified(t *testing.T) {
	procedures := generatedProcedures(t)
	if len(procedures) < 50 {
		t.Fatalf("only found %d procedure constants — the parser is probably looking in the wrong place", len(procedures))
	}

	var unclassified []string
	for _, p := range procedures {
		if publicProcedures[p] {
			continue
		}
		if _, ok := procedureMinRole[p]; ok {
			continue
		}
		unclassified = append(unclassified, p)
	}
	if len(unclassified) > 0 {
		t.Errorf("%d procedure(s) are neither public nor role-classified, so they are silently "+
			"owner-only:\n  %s\nAdd each to publicProcedures or procedureMinRole in role_gate.go.",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}
}

// TestNoClassificationForUnknownProcedure catches the reverse drift: a
// classification left behind for a procedure that no longer exists, which
// silently protects nothing.
func TestNoClassificationForUnknownProcedure(t *testing.T) {
	real := make(map[string]bool)
	for _, p := range generatedProcedures(t) {
		real[p] = true
	}
	for p := range procedureMinRole {
		if !real[p] {
			t.Errorf("procedureMinRole classifies %q, which no longer exists in the generated code", p)
		}
	}
	for p := range publicProcedures {
		if !real[p] {
			t.Errorf("publicProcedures lists %q, which no longer exists in the generated code", p)
		}
	}
	// accountantAlso is the one place a typo grants nothing and says
	// nothing: an entry naming a procedure that doesn't exist silently
	// leaves the accountant locked out of whatever was meant.
	for p := range accountantAlso {
		if !real[p] {
			t.Errorf("accountantAlso lists %q, which no longer exists in the generated code", p)
		}
	}
}

// generatedProcedures reads the procedure path constants out of the
// generated connect package. Parsing the generated source rather than
// maintaining a list here is the point: the test has to learn about a new
// RPC without anyone remembering to tell it.
func generatedProcedures(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "genpb", "stillhouse", "v1", "stillhousev1connect")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read generated connect package: %v", err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		{
			ast.Inspect(file, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil || !strings.HasPrefix(s, "/stillhouse.v1.") {
						continue
					}
					// Service paths ("/stillhouse.v1.AuthService") appear
					// too; procedures have a method segment.
					if strings.Count(s, "/") == 2 {
						out = append(out, s)
					}
				}
				return true
			})
		}
	}
	return out
}
