package server

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// strictJSONCodec is ConnectRPC's JSON codec with DiscardUnknown off.
//
// The default discards fields the message does not have, which is the
// right trade for a public API spanning versions and the wrong one here.
// QA posted {"gtin": "…"} to CreateProduct — which has no such field, the
// SKU details being set through UpdateProductSKU — and got a 200 with the
// value silently dropped. On a system whose numbers end up on an excise
// return, a request that half-worked and said it worked is worse than one
// that failed.
//
// Refusing costs forward compatibility with a newer client against an
// older server. Stillhouse is self-hosted and single-version; the web UI
// is served by the same binary that answers the RPC.
type strictJSONCodec struct{}

func (strictJSONCodec) Name() string { return "json" }

func (strictJSONCodec) Marshal(msg any) ([]byte, error) {
	m, ok := msg.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("%T is not a proto message", msg)
	}
	return protojson.MarshalOptions{}.Marshal(m)
}

func (strictJSONCodec) Unmarshal(data []byte, msg any) error {
	m, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("%T is not a proto message", msg)
	}
	// An empty body is a message with no fields set, which is what every
	// no-argument RPC sends.
	if len(data) == 0 {
		return nil
	}
	return protojson.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(data, m)
}
