package period

import (
	"testing"

	"ritm/internal/days"
)

func TestResolve(t *testing.T) {
	today := days.MustParse("2026-10-22") // Thursday
	cases := map[string]string{
		"today":                  "2026-10-22..2026-10-22",
		"last_7d":                "2026-10-16..2026-10-22",
		"this_week":              "2026-10-19..2026-10-22",
		"last_week":              "2026-10-12..2026-10-18",
		"this_month":             "2026-10-01..2026-10-22",
		"last_month":             "2026-09-01..2026-09-30",
		"ytd":                    "2026-01-01..2026-10-22",
		"2026-10-01":             "2026-10-01..2026-10-01",
		"2026-10-01..2026-10-05": "2026-10-01..2026-10-05",
		"last_3d":                "2026-10-20..2026-10-22",
	}
	for in, want := range cases {
		r, err := Resolve(in, today)
		if err != nil || r.String() != want {
			t.Errorf("%s: got %s %v, want %s", in, r, err, want)
		}
	}
	if _, err := Resolve("fortnight", today); err == nil {
		t.Error("expected error")
	}
	r, err := FromTo("last_month", "", today, 30)
	if err != nil || r.String() != "2026-09-01..2026-09-30" {
		t.Errorf("FromTo: %s %v", r, err)
	}
}
