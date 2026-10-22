package monitor

import (
	"net/http"
	"testing"
	"time"
)

func TestEvaluateThreshold(t *testing.T) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	last := time.Date(2026, 10, 6, 18, 14, 0, 0, time.UTC)
	imp := last.Add(12 * time.Hour)
	const stale = 14 * 24 * time.Hour

	cases := []struct {
		now    time.Time
		status string
		code   int
		msg    string
	}{
		{last.Add(30 * time.Minute), StatusOK, http.StatusOK, "Last Apple Health data less than an hour ago"},
		{last.Add(5 * time.Hour), StatusOK, http.StatusOK, "Last Apple Health data 5 hours ago"},
		{last.Add(stale - time.Second), StatusOK, http.StatusOK, "Last Apple Health data 13 days ago"},
		{last.Add(stale), StatusStale, http.StatusServiceUnavailable, "No new Apple Health data for 14 days"},
		{last.Add(16*24*time.Hour + 2*time.Hour), StatusStale, http.StatusServiceUnavailable, "No new Apple Health data for 16 days"},
	}
	for _, c := range cases {
		r := Evaluate(&last, &imp, stale, c.now, kyiv)
		if r.Status != c.status || r.HTTPStatus() != c.code || r.Message != c.msg {
			t.Errorf("now %s: got %s/%d %q, want %s/%d %q", c.now, r.Status, r.HTTPStatus(), r.Message, c.status, c.code, c.msg)
		}
		if r.StaleAfterDays != 14 {
			t.Errorf("stale_after_days = %v", r.StaleAfterDays)
		}
	}

	r := Evaluate(&last, &imp, stale, last.Add(16*24*time.Hour+2*time.Hour), kyiv)
	if *r.LastDataAt != "2026-10-06T21:14:00+03:00" || *r.AgeDays != 16.1 || *r.LastImportAt != "2026-10-07T09:14:00+03:00" {
		t.Errorf("report %+v (last_data_at %s, age %v)", r, *r.LastDataAt, *r.AgeDays)
	}
}

func TestEvaluateNoData(t *testing.T) {
	r := Evaluate(nil, nil, 72*time.Hour, time.Now(), time.UTC)
	if r.Status != StatusNoData || r.HTTPStatus() != http.StatusServiceUnavailable || r.LastDataAt != nil || r.AgeDays != nil || r.StaleAfterDays != 3 {
		t.Fatalf("%+v", r)
	}
	if f := Failed(time.Hour, time.Now(), time.UTC); f.HTTPStatus() != http.StatusInternalServerError || f.Status != StatusError {
		t.Fatalf("%+v", f)
	}
}

func TestParseMaxAge(t *testing.T) {
	for in, want := range map[string]time.Duration{"10d": 240 * time.Hour, "72h": 72 * time.Hour, "1d12h": 36 * time.Hour} {
		if got, err := ParseMaxAge(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "abc", "0h", "-2d", "d"} {
		if _, err := ParseMaxAge(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}
