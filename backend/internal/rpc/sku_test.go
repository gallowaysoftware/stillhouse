package rpc

import (
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/importer"
)

// A GTIN with a bad check digit is a transposed pair of digits, and the
// place that discovers it otherwise is a distributor's receiving dock.
// The check is pure arithmetic, so it is tested against known-good
// numbers rather than against the implementation.
func TestGTINCheckDigit(t *testing.T) {
	valid := []string{
		"0012345678905", // GS1's own worked example, GTIN-13
		"012345678905",  // the same as GTIN-12
		"00012345678905",
		"96385074", // GTIN-8
	}
	for _, g := range valid {
		if err := importer.ValidateGTIN(g); err != nil {
			t.Errorf("validateGTIN(%q) rejected a valid GTIN: %v", g, err)
		}
	}

	// Each of these is a valid GTIN with two adjacent digits swapped or
	// one digit changed — the errors a person actually makes.
	invalid := []string{
		"0012345678906", // wrong check digit
		"0012345687905", // transposition
		"001234567890",  // wrong length
		"00123456789051",
		"00123456789O5", // letter O for zero
	}
	for _, g := range invalid {
		if err := importer.ValidateGTIN(g); err == nil {
			t.Errorf("validateGTIN(%q) accepted an invalid GTIN", g)
		}
	}
}

// The SKU fields are the licensee's declarations, not Stillhouse's
// derivations. This pins that they round-trip and, more importantly,
// that nothing fills them in.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestProductSKUDetailsAreDeclaredNotDerived(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewProductService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	product := f.product(t, "SKU Whisky "+uuid.NewString()[:8], 750, 40)

	t.Run("a new product declares nothing", func(t *testing.T) {
		got, err := svc.GetProduct(f.ctx, connect.NewRequest(
			&stillhousev1.GetProductRequest{Id: product.ID.String()}))
		if err != nil {
			t.Fatalf("GetProduct: %v", err)
		}
		p := got.Msg.GetProduct()
		// Specifically: the spirit kind is Canadian-whisky-shaped and the
		// common name is still empty. Whether a spirit qualifies for a
		// standardised common name under Division 2 rests on how it was
		// made and how long it sat — it is the licensee's declaration,
		// and inferring it would be putting words on their label.
		if p.GetCommonName() != "" {
			t.Errorf("common_name was filled in as %q without anyone declaring it", p.GetCommonName())
		}
		if p.GetAgeStatement() != "" {
			t.Errorf("age_statement was filled in as %q", p.GetAgeStatement())
		}
	})

	t.Run("a bad GTIN is refused before it reaches a distributor", func(t *testing.T) {
		_, err := svc.UpdateProductSKU(f.ctx, connect.NewRequest(
			&stillhousev1.UpdateProductSKURequest{
				Id: product.ID.String(), Gtin: "0012345678906",
			}))
		if err == nil {
			t.Fatal("a GTIN with a wrong check digit was accepted")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got %v, want InvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("declared details round-trip", func(t *testing.T) {
		resp, err := svc.UpdateProductSKU(f.ctx, connect.NewRequest(
			&stillhousev1.UpdateProductSKURequest{
				Id:                product.ID.String(),
				Gtin:              "0012345678905",
				CspcCode:          "CSPC-112233",
				BottlesPerCase:    12,
				CasesPerLayer:     8,
				LayersPerPallet:   5,
				CaseGrossWeightKg: 14.4,
				CommonName:        "Canadian Whisky",
				AgeStatement:      "Aged 3 years",
				ContainerMarking:  "Product of Canada · 750 mL · 40% alc./vol.",
				CountryOfOrigin:   "Canada",
			}))
		if err != nil {
			t.Fatalf("UpdateProductSKU: %v", err)
		}
		p := resp.Msg.GetProduct()
		if p.GetGtin() != "0012345678905" || p.GetCspcCode() != "CSPC-112233" {
			t.Errorf("identifiers did not round-trip: %q %q", p.GetGtin(), p.GetCspcCode())
		}
		if p.GetBottlesPerCase() != 12 || p.GetLayersPerPallet() != 5 {
			t.Errorf("case configuration did not round-trip: %d per case, %d layers",
				p.GetBottlesPerCase(), p.GetLayersPerPallet())
		}
		if p.GetCommonName() != "Canadian Whisky" {
			t.Errorf("common_name %q", p.GetCommonName())
		}
		// The production fields must be untouched — this RPC is for how
		// it is sold, not what is in the bottle.
		if p.GetBottleSizeMl() != 750 || p.GetTargetAbvPct() != 40 {
			t.Errorf("updating SKU details changed the bottle: %d mL at %v%%",
				p.GetBottleSizeMl(), p.GetTargetAbvPct())
		}
	})

	t.Run("two SKUs cannot share a GTIN", func(t *testing.T) {
		other := f.product(t, "Other SKU "+uuid.NewString()[:8], 375, 43)
		_, err := svc.UpdateProductSKU(f.ctx, connect.NewRequest(
			&stillhousev1.UpdateProductSKURequest{
				Id: other.ID.String(), Gtin: "0012345678905",
			}))
		if err == nil {
			t.Fatal("two products were given the same GTIN — the wrong case ships")
		}
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("got %v, want AlreadyExists", connect.CodeOf(err))
		}
	})
}
