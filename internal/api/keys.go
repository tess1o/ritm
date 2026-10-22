package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/auth"
)

// NewAPIKey creates an API key.
type NewAPIKey struct {
	Name  string `json:"name" minLength:"1" maxLength:"80" example:"iPhone Shortcut"`
	Scope string `json:"scope" enum:"ingest,monitor,mcp" doc:"ingest: upload exports and read their status; monitor: data freshness (Uptime Kuma); mcp: the MCP server"`
}

// CreatedAPIKey is a new key with its secret, which is shown only once (KEY-1).
type CreatedAPIKey struct {
	Key    auth.APIKey `json:"key"`
	Secret string      `json:"secret" example:"ritm_3x…" doc:"Send as Authorization: Bearer <secret>. Not retrievable later."`
}

// IDPath is the {id} path parameter (exported: Huma reads parameters of exported embedded structs only).
type IDPath struct {
	ID int64 `path:"id" minimum:"1"`
}

func (s *Server) registerKeys() {
	tags := []string{"API keys"}
	register(s, sessionOnly, huma.Operation{OperationID: "listApiKeys", Method: http.MethodGet, Path: "/api-keys", Tags: tags,
		Summary: "List API keys", Description: "All keys of the user, newest first, including revoked ones (KEY-3)."},
		func(ctx context.Context, _ *struct{}) (*body[Items[auth.APIKey]], error) {
			keys, err := s.Auth.ListKeys(ctx, user(ctx).ID)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(Items[auth.APIKey]{Items: keys}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "createApiKey", Method: http.MethodPost, Path: "/api-keys", Tags: tags,
		Summary: "Create an API key", DefaultStatus: http.StatusCreated,
		Description: "Keys are `ritm_` + 32 random bytes; only a hash is stored, so the secret is returned once (KEY-1).", Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *struct{ Body NewAPIKey }) (*body[CreatedAPIKey], error) {
			plain, k, err := s.Auth.CreateKey(ctx, user(ctx).ID, in.Body.Name, in.Body.Scope)
			if err != nil {
				return nil, s.authProblem(ctx, err)
			}
			return ok(CreatedAPIKey{Key: k, Secret: plain}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "revokeApiKey", Method: http.MethodDelete, Path: "/api-keys/{id}", Tags: tags,
		Summary: "Revoke an API key", DefaultStatus: http.StatusNoContent, Description: "Takes effect immediately (KEY-3).", Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			err := s.Auth.RevokeKey(ctx, user(ctx).ID, in.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("key not found or already revoked")
			}
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return nil, nil
		})
}
