package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/derive"
	"ritm/internal/tenant"
)

// ServerSettings are the environment settings the UI shows (read-only).
type ServerSettings struct {
	StaleAfterHours  float64 `json:"stale_after_hours" doc:"RITM_STALE_AFTER"`
	DeltaWindowHours float64 `json:"delta_window_hours" doc:"RITM_DELTA_WINDOW"`
	Timezone         string  `json:"timezone" doc:"RITM_TZ, used while the user has no time zone set"`
	BaseURL          string  `json:"base_url" doc:"RITM_BASE_URL, empty when unset"`
	MaxUploadMB      int64   `json:"max_upload_mb" doc:"RITM_MAX_UPLOAD_MB"`
}

// SettingsResponse is the user's settings, their resolved values and the server's settings.
type SettingsResponse struct {
	Settings derive.Settings  `json:"settings"`
	Resolved *derive.Resolved `json:"resolved" doc:"Settings with automatic values filled in from the data"`
	Server   ServerSettings   `json:"server"`
}

func (s *Server) settingsOf(ctx context.Context, t *tenant.Tenant) (*body[SettingsResponse], error) {
	st, err := t.Query.Settings(ctx)
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	res, err := t.Query.Resolved(ctx)
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	return ok(SettingsResponse{Settings: st, Resolved: res, Server: ServerSettings{
		StaleAfterHours:  s.Cfg.StaleAfter.Hours(),
		DeltaWindowHours: s.Cfg.DeltaWindow.Hours(),
		Timezone:         s.Cfg.TZ.String(),
		BaseURL:          s.Cfg.BaseURL,
		MaxUploadMB:      s.Cfg.MaxUploadBytes >> 20,
	}}), nil
}

func (s *Server) registerSettings() {
	tags := []string{"Settings"}
	register(s, sessionOnly, huma.Operation{OperationID: "getSettings", Method: http.MethodGet, Path: "/settings", Tags: tags,
		Summary: "Settings", Description: "Goals, HR zones, load model, source priority, waking hours and other settings."},
		func(ctx context.Context, _ *struct{}) (*body[SettingsResponse], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			return s.settingsOf(ctx, t)
		})

	register(s, sessionOnly, huma.Operation{OperationID: "putSettings", Method: http.MethodPut, Path: "/settings", Tags: tags,
		Summary: "Replace settings", Description: "Replaces all settings. When a setting that feeds derived data changes, derived tables and insights are recomputed before the response.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *struct{ Body derive.Settings }) (*body[SettingsResponse], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			st := in.Body
			st.MainWorkoutType = strings.TrimSpace(st.MainWorkoutType)
			if err := st.Validate(); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			prev, err := t.Query.Settings(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			if err := derive.SaveSettings(ctx, t.DB, st); err != nil {
				return nil, s.problem(ctx, err)
			}
			t.UseSettings(st)
			if prev.AffectsDerived(st) {
				if err := t.Run(func(ctx context.Context) error { return t.Derive.Recompute(ctx, "") }); err != nil {
					return nil, s.problem(ctx, err)
				}
			}
			return s.settingsOf(ctx, t)
		})
}
