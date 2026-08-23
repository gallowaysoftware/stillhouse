package rpc

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// neverAssigned lists B266Report fields that projectB266 legitimately does
// not set, each with the reason. Everything else must be assigned
// somewhere, or it ships as a zero.
var neverAssigned = map[string]string{
	"Notes": "appended by a separate pass over the totals rather than set here",
}

// TestEveryB266FieldIsAssigned catches a field added to the report and
// never filled in.
//
// Three times in this session an edit that inserted an assignment failed
// to match its anchor after gofmt realigned the block, the build stayed
// green — a struct literal with a missing field compiles perfectly — and
// the field shipped as zero. Once that was a B266 figure. A test that
// reads the descriptor and the source is cheap, and it is the only thing
// that distinguishes "deliberately absent" from "silently forgotten".
//
// Deliberately a source scan rather than a live comparison: the failure
// is a missing line of code, and that is what this looks for.
func TestEveryB266FieldIsAssigned(t *testing.T) {
	src, err := os.ReadFile("b266_projection.go")
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	text := string(src)

	var missing []string
	fields := (&stillhousev1.B266Report{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		name := goFieldName(fd)
		// Both shapes count: a struct-literal key and a plain assignment.
		// The opening balances are computed after the literal, from the
		// closing ones, so only the second form appears for them.
		assigned := strings.Contains(text, name+":") ||
			strings.Contains(text, "."+name+" =")
		if why, ok := neverAssigned[name]; ok {
			if assigned {
				t.Errorf("%s is listed as never assigned (%s) but is assigned — "+
					"remove it from the list", name, why)
			}
			continue
		}
		if !assigned {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d B266 field(s) are never assigned in projectB266, so they "+
			"ship as zero:\n  %s\nAdd the assignment, or list it in "+
			"neverAssigned with the reason.", len(missing), strings.Join(missing, "\n  "))
	}
}

// goFieldName is protoc-gen-go's field naming: underscores removed, each
// following letter capitalised, with the same digit handling.
func goFieldName(fd protoreflect.FieldDescriptor) string {
	var b strings.Builder
	up := true
	for _, r := range string(fd.Name()) {
		if r == '_' {
			up = true
			continue
		}
		if up && r >= 'a' && r <= 'z' {
			b.WriteRune(r - 32)
		} else {
			b.WriteRune(r)
		}
		up = false
	}
	return b.String()
}
