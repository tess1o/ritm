package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/auth"
	"ritm/internal/monitor"
)

type monitorInput struct {
	MaxAge string `query:"max_age" example:"10d" doc:"Override the stale threshold for this request, e.g. 10d, 72h or 1d12h"`
}

type monitorOutput struct {
	Status int
	Body   monitor.FreshnessReport
}

func (s *Server) registerMonitor() {
	op := huma.Operation{OperationID: "getMonitorFreshness", Method: http.MethodGet, Path: "/monitor/freshness", Tags: []string{"Monitoring"},
		Summary: "Data freshness for Uptime Kuma",
		Description: "200 `ok` while data is fresh; 503 `stale` once no new Apple Health data arrived for the threshold (Settings, RITM_STALE_AFTER " +
			"or `max_age`), 503 `no_data` before the first import, 500 `error` when the database is unavailable. The body has only dates and counts.",
		Responses: map[string]*huma.Response{
			"503": {Description: "Stale or no data", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/FreshnessReport"}}}},
		},
		Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests}}
	register(s, keyOnly(auth.ScopeMonitor), op, func(ctx context.Context, in *monitorInput) (*monitorOutput, error) {
		now := s.Clock.Now()
		var maxAge time.Duration
		if in.MaxAge != "" {
			d, err := monitor.ParseMaxAge(in.MaxAge)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			maxAge = d
		}
		staleAfter := s.Cfg.StaleAfter
		if maxAge > 0 {
			staleAfter = maxAge
		}
		loc := s.Cfg.TZ
		failed := func(err error) (*monitorOutput, error) {
			s.Log.Error("monitor: read freshness", "user", user(ctx).Username, "err", err)
			rep := monitor.Failed(staleAfter, now, loc)
			return &monitorOutput{Status: rep.HTTPStatus(), Body: rep}, nil
		}
		t, err := s.Tenants.Get(ctx, user(ctx))
		if err != nil {
			return failed(err)
		}
		loc = t.Zone.Location()
		f, err := t.Query.Freshness(ctx, s.Cfg.StaleAfter)
		if err != nil {
			return failed(err)
		}
		if maxAge == 0 {
			staleAfter = time.Duration(f.StaleAfterH * float64(time.Hour))
		}
		t.Query.MarkChecked(now)
		rep := monitor.Evaluate(f.LastDataAt, f.LastImportAt, staleAfter, now, loc)
		return &monitorOutput{Status: rep.HTTPStatus(), Body: rep}, nil
	})
}
