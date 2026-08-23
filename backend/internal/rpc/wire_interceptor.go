package rpc

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/gallowaysoftware/stillhouse/backend/internal/wire"
)

// NewFloatRoundingInterceptor states every figure leaving Stillhouse to a
// fixed number of decimal places, so a caller receives the quantity that
// was meant rather than the residue of the arithmetic that produced it.
//
// This is QA finding F17. A removal of 60 bottles at 40 % in 750 mL came
// back as 18.000000000000004 LAA and 0.8399999999999999 of duty. The web
// UI rounded at display, so the noise was invisible in the one place
// somebody would have noticed it and fully present in every other: the
// MCP tools, curl against the endpoints, and anything a licensee wrote
// against the API. A figure on an excise return that reads
// 0.8399999999999999 does not inspire confidence in the rest of it.
//
// It sits at the boundary rather than in each of a hundred and forty
// conversion sites, because a rule applied in one place is a rule and a
// rule applied in a hundred and forty is a habit somebody will break.
// Nothing stored or computed changes: the rounding happens to the copy on
// its way out. See internal/wire for why six places.
func NewFloatRoundingInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			if err != nil || res == nil {
				return res, err
			}
			if m, ok := res.Any().(proto.Message); ok {
				wire.Message(m)
			}
			return res, nil
		}
	}
}
