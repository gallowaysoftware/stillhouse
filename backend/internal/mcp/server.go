// Package mcp serves the Stillhouse RPC surface over the Model Context
// Protocol so an LLM (Claude, etc.) can read and capture distillery
// activity hands-free — typically from a phone while the operator is
// standing at the still.
//
// The handler is mounted at /mcp by the parent http server. Each
// request authenticates via Authorization: Bearer sh_... (issued by
// cmd/mcp-token); the resolved user is bound into a per-session SDK
// Server so every tool call runs under that user's RLS tenant scope.
//
// Tools are intentionally a narrow slice of the full ConnectRPC API —
// the things an operator does with wet hands. Back-office work
// (bottling, removals, B266 generation) still happens in the web UI.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// Deps bundles every RPC service the MCP tools call into. Same instances
// the ConnectRPC mux uses, so audit/RLS/role-gate behaviour is identical.
type Deps struct {
	Queries       *sqlcgen.Queries
	Bulk          *rpc.BulkService
	Barrel        *rpc.BarrelService
	Recipe        *rpc.RecipeService
	Product       *rpc.ProductService
	Fermentation  *rpc.FermentationService
	Mash          *rpc.MashService
	B266          *rpc.B266Service
	Alcoholometry *rpc.AlcoholometryService
	Logger        *slog.Logger
}

// NewHandler returns an http.Handler that speaks MCP over Streamable
// HTTP. The handler authenticates each session via bearer token; nil
// is returned to the caller (→ HTTP 400) on a missing / bad token.
func NewHandler(d Deps) http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		tok, err := rpc.ExtractBearer(r.Header)
		if err != nil {
			return nil
		}
		user, err := rpc.LookupBearer(r.Context(), d.Queries, tok)
		if err != nil {
			return nil
		}
		return newSessionServer(d, user)
	}, nil)
}

// newSessionServer constructs an SDK Server with every tool closed over
// the authenticated user. One Server per session; tools are stateless.
func newSessionServer(d Deps, user sqlcgen.User) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "stillhouse",
		Version: "0.1.0",
	}, &mcpsdk.ServerOptions{
		Instructions: "Stillhouse — Canadian craft distillery management. " +
			"Use the read tools (list_*, get_*) to inspect bulk inventory, " +
			"barrels, recipes, products, and B266 status. Use the write " +
			"tools to capture activity at the still: fill_barrel, " +
			"dump_barrel, regauge_barrel, add_fermentation_reading, " +
			"add_mash_reading. All volumes are litres, ABV is percent, " +
			"LAA is litres of absolute alcohol.",
		Logger: d.Logger,
	})
	registerReadTools(s, d, user)
	registerWriteTools(s, d, user)
	return s
}

// guard injects the authenticated user into the context for a downstream
// RPC call AND enforces the role gate for the procedure being stood in
// for.
//
// /mcp is mounted as a plain http.Handler, outside the ConnectRPC
// interceptor chain, so the gate never runs for it. Tools used to inject
// the user and call the service method directly, and those methods carry
// no role check of their own — so a viewer could mint a token
// (IssueAPIToken is viewer-level by design so everyone can manage their
// own) and use it to fill, dump and regauge barrels through this surface.
// Keeping the procedure string here means role_gate.go stays the single
// source of truth for who may do what.
func guard(ctx context.Context, user sqlcgen.User, procedure string) (context.Context, error) {
	if err := rpc.AuthorizeProcedure(procedure, user.Role); err != nil {
		return nil, err
	}
	return rpc.WithUser(ctx, user), nil
}

// jsonResult marshals a proto message into a textual MCP result. We
// emit protojson rather than plain encoding/json so enum values render
// as their string names ("BARREL_EVENT_KIND_FILL") rather than ints,
// and oneofs/optional fields behave correctly.
//
// EmitUnpopulated is on so empty arrays and zero numerics render
// explicitly ("recipes": [], "volume_l": 0). An LLM that sees `{}` has
// no way to tell "no rows" from "field doesn't exist", and `_set:
// true` flags paired with elided zero values were the source of QA
// finding F8 — both go away when we emit zero values directly.
func jsonResult(m proto.Message) *mcpsdk.CallToolResult {
	b, err := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
		Indent:          "  ",
	}.Marshal(m)
	if err != nil {
		return errResult(err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}
}

// jsonResultRaw is for ad-hoc result shapes (e.g. dashboard rollup)
// that aren't protobufs.
func jsonResultRaw(v any) *mcpsdk.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}
}

// errResult turns an error into an MCP-visible failure. Setting IsError
// lets the model see the failure and retry / report it.
//
// When the underlying error is a typed *connect.Error we prefix the
// code (e.g. "NotFound: fermentation run not found") so the model can
// distinguish retryable problems (Internal, Unavailable) from caller
// mistakes (InvalidArgument, NotFound, FailedPrecondition) without
// regex-matching the message text.
func errResult(err error) *mcpsdk.CallToolResult {
	msg := err.Error()
	var ce *connect.Error
	if errors.As(err, &ce) {
		msg = ce.Code().String() + ": " + ce.Message()
	}
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}
}
