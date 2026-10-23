// Package monitor turns a user's data freshness into the report polled by Uptime Kuma.
// Ritm sends no notifications itself: the HTTP status tells Kuma whether to alert.
package monitor

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"ritm/internal/config"
	"ritm/internal/mathx"
)

const (
	StatusOK     = "ok"
	StatusStale  = "stale"
	StatusNoData = "no_data"
	StatusError  = "error"
)

// FreshnessReport is the body of GET /api/v1/monitor/freshness. It holds only dates and counts,
// never health values.
type FreshnessReport struct {
	Status         string   `json:"status" enum:"ok,stale,no_data,error"`
	Message        string   `json:"message"`
	LastDataAt     *string  `json:"last_data_at"`
	AgeDays        *float64 `json:"age_days"`
	StaleAfterDays float64  `json:"stale_after_days"`
	LastImportAt   *string  `json:"last_import_at"`
	CheckedAt      string   `json:"checked_at"`
}

// HTTPStatus maps the report to the status code Kuma checks: 2xx only for fresh data.
func (r FreshnessReport) HTTPStatus() int {
	switch r.Status {
	case StatusOK:
		return http.StatusOK
	case StatusStale, StatusNoData:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Evaluate reports data as stale when now − lastData ≥ staleAfter. Times are rendered
// in loc with their offset.
func Evaluate(lastData, lastImport *time.Time, staleAfter time.Duration, now time.Time, loc *time.Location) FreshnessReport {
	r := FreshnessReport{
		StaleAfterDays: mathx.Round(staleAfter.Hours()/24, 2),
		LastDataAt:     stamp(lastData, loc),
		LastImportAt:   stamp(lastImport, loc),
		CheckedAt:      now.In(loc).Truncate(time.Second).Format(time.RFC3339),
	}
	if lastData == nil {
		r.Status, r.Message = StatusNoData, "No Apple Health data imported yet"
		return r
	}
	age := max(now.Sub(*lastData), 0)
	days := mathx.Round(age.Hours()/24, 1)
	r.AgeDays = &days
	if age >= staleAfter {
		r.Status, r.Message = StatusStale, "No new Apple Health data for "+span(age)
	} else {
		r.Status, r.Message = StatusOK, "Last Apple Health data "+span(age)+" ago"
	}
	return r
}

// Failed is the report for an internal error (database unreachable).
func Failed(staleAfter time.Duration, now time.Time, loc *time.Location) FreshnessReport {
	return FreshnessReport{Status: StatusError, Message: "Data freshness unavailable (database error)",
		StaleAfterDays: mathx.Round(staleAfter.Hours()/24, 2), CheckedAt: now.In(loc).Truncate(time.Second).Format(time.RFC3339)}
}

// ParseMaxAge parses the max_age query parameter, e.g. "10d", "72h" or "1d12h".
func ParseMaxAge(s string) (time.Duration, error) {
	d, err := config.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errors.New("max_age must be a positive duration such as 10d or 72h")
	}
	return d, nil
}

func span(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func stamp(t *time.Time, loc *time.Location) *string {
	if t == nil {
		return nil
	}
	s := t.In(loc).Truncate(time.Second).Format(time.RFC3339)
	return &s
}
