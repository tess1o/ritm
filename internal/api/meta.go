package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/mcpserver"
)

// Health is the liveness report of GET /healthz.
type Health struct {
	Status  string `json:"status" enum:"ok,error"`
	Version string `json:"version"`
}

type healthOutput struct {
	Status int
	Body   Health
}

// McpInfo describes the MCP server for the AI-assistants page.
type McpInfo struct {
	Path    string             `json:"path" example:"/mcp" doc:"Path of the MCP endpoint (streamable HTTP, Bearer key with scope read)"`
	BaseURL string             `json:"base_url" doc:"RITM_BASE_URL, empty when unset"`
	Catalog *mcpserver.Catalog `json:"catalog"`
}

func (s *Server) registerMeta() {
	op := huma.Operation{OperationID: "health", Method: http.MethodGet, Path: "/healthz", Tags: []string{"Meta"},
		Summary: "Liveness probe", Description: "200 when the server is up and the accounts database answers, 503 otherwise. No authentication.",
		Responses: map[string]*huma.Response{"503": {Description: "Database unavailable"}}}
	describe(&op, public)
	huma.Register(s.api, op, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := s.AppDB.Ping(ctx); err != nil {
			return &healthOutput{Status: http.StatusServiceUnavailable, Body: Health{Status: "error", Version: s.Version}}, nil
		}
		return &healthOutput{Status: http.StatusOK, Body: Health{Status: "ok", Version: s.Version}}, nil
	})

	register(s, sessionOnly, huma.Operation{OperationID: "getMcpCatalog", Method: http.MethodGet, Path: "/mcp/catalog", Tags: []string{"Meta"},
		Summary: "MCP server catalog", Description: "The MCP endpoint and the tools, resources and prompts AI assistants can use."},
		func(ctx context.Context, _ *struct{}) (*body[McpInfo], error) {
			c, err := s.mcp.Catalog()
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(McpInfo{Path: "/mcp", BaseURL: s.Cfg.BaseURL, Catalog: c}), nil
		})
}
