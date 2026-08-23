package server

import (
	"testing"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// QA-3: {"gtin": "…"} posted to CreateProduct, which has no gtin field,
// returned 200 with the value silently dropped. On a system whose figures
// end up on an excise return, a request that half-worked and said it
// worked is worse than one that failed.
func TestStrictJSONRefusesUnknownFields(t *testing.T) {
	var codec strictJSONCodec

	var msg stillhousev1.CreateProductRequest
	if err := codec.Unmarshal([]byte(`{"name":"Rye"}`), &msg); err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}
	if msg.GetName() != "Rye" {
		t.Errorf("name = %q", msg.GetName())
	}

	// The real finding: gtin is set through UpdateProductSKU, so this is
	// a plausible mistake rather than a contrived one.
	var bad stillhousev1.CreateProductRequest
	if err := codec.Unmarshal([]byte(`{"name":"Rye","gtin":"06285010000019"}`), &bad); err == nil {
		t.Error("a field the message does not have was accepted and discarded")
	}
}

// Every no-argument RPC posts an empty body, and several post `{}`.
func TestStrictJSONAcceptsEmptyBodies(t *testing.T) {
	var codec strictJSONCodec
	for _, body := range []string{"", "{}"} {
		var msg stillhousev1.ListProductsRequest
		if err := codec.Unmarshal([]byte(body), &msg); err != nil {
			t.Errorf("body %q was refused: %v", body, err)
		}
	}
}

func TestStrictJSONRoundTrips(t *testing.T) {
	var codec strictJSONCodec
	in := &stillhousev1.CreateProductRequest{Name: "Rye", BottleSizeMl: 750}
	b, err := codec.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out stillhousev1.CreateProductRequest
	if err := codec.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.GetName() != in.GetName() || out.GetBottleSizeMl() != in.GetBottleSizeMl() {
		t.Errorf("round trip lost something: %+v", &out)
	}
}
