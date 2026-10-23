package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/auth"
	"ritm/internal/httpx"
)

// Account is the signed-in user with their clock and the server's version.
type Account struct {
	LocalClock
	User    auth.User `json:"user"`
	Version string    `json:"version" doc:"Ritm build version"`
}

// PasswordChange sets a new password.
type PasswordChange struct {
	CurrentPassword string `json:"current_password" minLength:"1" maxLength:"72"`
	NewPassword     string `json:"new_password" minLength:"8" maxLength:"72"`
}

// PasswordConfirmation re-authenticates a destructive action.
type PasswordConfirmation struct {
	Password string `json:"password" minLength:"1" maxLength:"72" doc:"The account's current password"`
}

type passwordChangeInput struct {
	httpRequest
	Body PasswordChange
}

type confirmInput struct {
	httpRequest
	Body PasswordConfirmation
}

func (s *Server) confirm(ctx context.Context, in *confirmInput) (auth.User, error) {
	u := user(ctx)
	if err := s.Auth.CheckPassword(ctx, httpx.ClientIP(in.req), u.ID, in.Body.Password); err != nil {
		return u, s.authProblem(ctx, err)
	}
	return u, nil
}

func (s *Server) registerAccount() {
	tags := []string{"Account"}
	register(s, sessionOnly, huma.Operation{OperationID: "getAccount", Method: http.MethodGet, Path: "/account", Tags: tags,
		Summary: "Signed-in account", Description: "The user, their time zone and today, and the build version."},
		func(ctx context.Context, _ *struct{}) (*body[Account], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			return ok(Account{LocalClock: s.clock(t), User: user(ctx), Version: s.Version}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "changePassword", Method: http.MethodPut, Path: "/account/password", Tags: tags,
		Summary: "Change password", DefaultStatus: http.StatusNoContent,
		Description: "Requires the current password; the user's other sessions end.", Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests}},
		func(ctx context.Context, in *passwordChangeInput) (*struct{}, error) {
			if err := auth.ValidatePassword(in.Body.NewPassword); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			u := user(ctx)
			if err := s.Auth.CheckPassword(ctx, httpx.ClientIP(in.req), u.ID, in.Body.CurrentPassword); err != nil {
				return nil, s.authProblem(ctx, err)
			}
			if err := s.Auth.SetPassword(ctx, u.ID, in.Body.NewPassword, auth.SessionToken(in.req)); err != nil {
				return nil, s.authProblem(ctx, err)
			}
			return nil, nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "resetAccountData", Method: http.MethodPost, Path: "/account/reset", Tags: tags,
		Summary: "Delete all health data", DefaultStatus: http.StatusNoContent,
		Description: "Replaces the user's database with an empty one: imports, samples, workouts, insights, notes and settings go; the account and API keys stay.",
		Errors:      []int{http.StatusTooManyRequests}},
		func(ctx context.Context, in *confirmInput) (*struct{}, error) {
			u, err := s.confirm(ctx, in)
			if err != nil {
				return nil, err
			}
			if _, err := s.Tenants.ResetData(context.WithoutCancel(ctx), u); err != nil {
				return nil, s.problem(ctx, err)
			}
			s.Log.Info("user data reset", "user", u.Username)
			return nil, nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "deleteAccount", Method: http.MethodDelete, Path: "/account", Tags: tags,
		Summary: "Delete the account", DefaultStatus: http.StatusNoContent,
		Description: "Deletes the user, their sessions, API keys and data.", Errors: []int{http.StatusTooManyRequests}},
		func(ctx context.Context, in *confirmInput) (*cookieOutput, error) {
			u, err := s.confirm(ctx, in)
			if err != nil {
				return nil, err
			}
			if err := s.Tenants.DeleteUser(context.WithoutCancel(ctx), u); err != nil {
				return nil, s.problem(ctx, err)
			}
			s.Log.Info("user deleted their account", "user", u.Username)
			return &cookieOutput{SetCookie: auth.ClearedSessionCookie(s.secure(in.req))}, nil
		})
}
