// Package mcp serves the Stillhouse RPC surface over the Model Context
// Protocol so an LLM (Claude, etc.) can read and capture distillery
// activity hands-free — typically from a phone while the operator is
// standing at the still.
//
// The handler is mounted at /mcp by the parent http server. Each
// request authenticates via Authorization: Bearer sh_... (issued in the web
// UI); the resolved user is bound into a per-session SDK Server so every
// tool call runs under that user's RLS tenant scope.
//
// Tools cover what an operator does on the floor, grain to cuts. The
// figures an operator signs for — production gauges, bottling, removals,
// B266 generation — still happen in the web UI.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
	"github.com/gallowaysoftware/stillhouse/backend/internal/wire"
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
	Distillation  *rpc.DistillationService
	Engineering   *rpc.EngineeringService
	Mash          *rpc.MashService
	B266          *rpc.B266Service
	WorkOrder     *rpc.WorkOrderService
	Alcoholometry *rpc.AlcoholometryService
	Tenant        *rpc.TenantService
	Instrument    *rpc.InstrumentService
	Material      *rpc.MaterialService
	Equipment     *rpc.EquipmentService
	Alert         *rpc.AlertService
	Logger        *slog.Logger
}

// sessionIdle bounds how long an abandoned session is kept. It outlasts a
// brew day's gaps; access is decided per request, not by this.
const sessionIdle = 24 * time.Hour

// NewHandler returns an http.Handler that speaks MCP over Streamable HTTP.
// The bearer token is checked on every request, not only the one that opens
// the session: otherwise the session id alone is a credential, and revoking
// the token leaves it working.
func NewHandler(d Deps) http.Handler {
	h := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil {
			return nil
		}
		user, ok := info.Extra[userExtra].(sqlcgen.User)
		if !ok {
			return nil
		}
		only, ok := toolset(r.URL.Query().Get("toolset"))
		if !ok {
			return nil
		}
		return newSessionServer(d, user, only)
	}, &mcpsdk.StreamableHTTPOptions{SessionTimeout: sessionIdle})
	return auth.RequireBearerToken(d.verifyToken, &auth.RequireBearerTokenOptions{
		// LookupBearer enforces expiry, and some tokens never expire.
		AllowMissingExpiration: true,
	})(h)
}

const userExtra = "user"

// verifyToken resolves a bearer token. The id it returns binds a session to
// the person and the role the session's tools were built for, so a changed
// role ends the session rather than leaving the old one in force.
func (d Deps) verifyToken(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	user, err := rpc.LookupBearer(ctx, d.Queries, token)
	if errors.Is(err, rpc.ErrBadBearer) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		if d.Logger != nil {
			d.Logger.Error("mcp: token lookup", "err", err)
		}
		return nil, errors.New("internal error")
	}
	return &auth.TokenInfo{
		UserID: user.ID.String() + ":" + string(user.Role),
		Extra:  map[string]any{userExtra: user},
	}, nil
}

// newSessionServer constructs an SDK Server with every tool closed over
// the authenticated user. One Server per session; tools are stateless.
func newSessionServer(d Deps, user sqlcgen.User, only map[string]bool) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "stillhouse",
		Version: "0.1.0",
	}, &mcpsdk.ServerOptions{
		Instructions: Instructions,
		Logger:       d.Logger,
	})
	registerReadTools(&registrar{server: s, only: only, readOnly: true}, d, user)
	registerWriteTools(&registrar{server: s, only: only}, d, user)
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
	b, err := marshalProto(m)
	if err != nil {
		return errResult(err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}
}

func marshalProto(m proto.Message) ([]byte, error) {
	// The MCP tools call the service implementations directly, so they
	// never pass through the ConnectRPC interceptors — including the one
	// that strips floating-point residue. Without this line an assistant
	// reading the tank reads 18.000000000000004 LAA. See internal/wire.
	wire.Message(m)
	return protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
		Indent:          "  ",
	}.Marshal(m)
}

// jsonResultRaw is for ad-hoc result shapes (e.g. dashboard rollup)
// that aren't protobufs.
func jsonResultRaw(v any) *mcpsdk.CallToolResult {
	v = wire.Struct(v)
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
