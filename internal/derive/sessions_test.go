package derive

import (
	"context"
	"slices"
	"testing"
	"time"

	"ritm/internal/days"
	"ritm/internal/store"
)

func TestSessionsLabel(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"Walking"}, "Walking"},
		{[]string{"Functional Strength Training", "Walking"}, "Functional Strength Training + Walking"},
		{[]string{"A", "B", "C"}, "3 workout types"},
	} {
		if got := SessionsLabel(c.names); got != c.want {
			t.Errorf("SessionsLabel(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

func TestSessionTypes(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenUser(ctx, t.TempDir(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const fst, walk = "HKWorkoutActivityTypeFunctionalStrengthTraining", "HKWorkoutActivityTypeWalking"
	// One ISO week (Mon 2026-10-19): strength on Mon, Wed, Fri and a walk on Sat.
	for _, w := range []struct {
		typ string
		day int
	}{{fst, 19}, {fst, 21}, {fst, 23}, {walk, 24}} {
		start := time.Date(2026, 10, w.day, 12, 0, 0, 0, time.UTC).UnixMilli()
		if _, err := db.W.ExecContext(ctx, `INSERT INTO workouts (activity_type, source_name, start_ms, end_ms, tz_offset_min, duration_s, import_id)
			VALUES (?, 'Watch', ?, ?, 0, 3600, 'i')`, w.typ, start, start+3600_000); err != nil {
			t.Fatal(err)
		}
	}

	// A settings row saved before session_types loads its single main type.
	if err := store.SetSetting(ctx, db.W, store.AppSettingsKey, map[string]any{"main_workout_type": walk}); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(ctx, db.RO)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.SessionTypes, []string{walk}) {
		t.Fatalf("migrated session types %v", s.SessionTypes)
	}

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	auto, err := Resolve(ctx, db.RO, DefaultSettings(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(auto.SessionTypesEffective, []string{fst}) || auto.SessionsLabel != "Functional Strength Training" {
		t.Fatalf("auto: %v %q", auto.SessionTypesEffective, auto.SessionsLabel)
	}

	r := days.Range{From: days.MustParse("2026-10-19"), To: days.MustParse("2026-10-25")}
	today := days.MustParse("2026-10-26")
	for _, c := range []struct {
		types []string
		want  int
	}{{[]string{fst}, 3}, {[]string{fst, walk}, 4}} {
		weeks, err := WeeklySessions(ctx, db.RO, r, 3, c.types, today)
		if err != nil {
			t.Fatal(err)
		}
		if len(weeks) != 1 || weeks[0].Sessions != c.want || !weeks[0].Met {
			t.Fatalf("types %v: weeks %+v", c.types, weeks)
		}
	}
}
