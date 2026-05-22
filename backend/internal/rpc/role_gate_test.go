package rpc

import (
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

func TestCheckRole(t *testing.T) {
	cases := []struct {
		name      string
		procedure string
		hasUser   bool
		role      sqlcgen.UserRole
		wantCode  connect.Code
		wantOK    bool
	}{
		{
			name:      "public procedure bypasses with no user",
			procedure: "/stillhouse.v1.AuthService/Login",
			wantOK:    true,
		},
		{
			name:      "missing user on private procedure rejects",
			procedure: "/stillhouse.v1.RecipeService/ListRecipes",
			wantCode:  connect.CodeUnauthenticated,
		},
		{
			name:      "viewer allowed read",
			procedure: "/stillhouse.v1.RecipeService/ListRecipes",
			hasUser:   true,
			role:      sqlcgen.UserRoleViewer,
			wantOK:    true,
		},
		{
			name:      "viewer blocked from write",
			procedure: "/stillhouse.v1.RecipeService/CreateRecipe",
			hasUser:   true,
			role:      sqlcgen.UserRoleViewer,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name:      "operator allowed write",
			procedure: "/stillhouse.v1.MashService/CreateMashRun",
			hasUser:   true,
			role:      sqlcgen.UserRoleOperator,
			wantOK:    true,
		},
		{
			name:      "operator blocked from owner-only",
			procedure: "/stillhouse.v1.UserService/CreateUser",
			hasUser:   true,
			role:      sqlcgen.UserRoleOperator,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name:      "owner allowed owner-only",
			procedure: "/stillhouse.v1.B266Service/SubmitB266",
			hasUser:   true,
			role:      sqlcgen.UserRoleOwner,
			wantOK:    true,
		},
		{
			name:      "viewer can always change own password",
			procedure: "/stillhouse.v1.UserService/ChangeMyPassword",
			hasUser:   true,
			role:      sqlcgen.UserRoleViewer,
			wantOK:    true,
		},
		{
			name:      "unlisted procedure fails closed for operator",
			procedure: "/stillhouse.v1.RecipeService/MysteryFuture",
			hasUser:   true,
			role:      sqlcgen.UserRoleOperator,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name:      "unlisted procedure allowed for owner",
			procedure: "/stillhouse.v1.RecipeService/MysteryFuture",
			hasUser:   true,
			role:      sqlcgen.UserRoleOwner,
			wantOK:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkRole(c.procedure, c.hasUser, c.role)
			if c.wantOK {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			var ce *connect.Error
			if !errors.As(err, &ce) {
				t.Fatalf("expected *connect.Error, got %T (%v)", err, err)
			}
			if ce.Code() != c.wantCode {
				t.Fatalf("expected code %v, got %v (%v)", c.wantCode, ce.Code(), err)
			}
		})
	}
}
