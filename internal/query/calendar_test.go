package query

import (
	"context"
	"reflect"
	"testing"
	"time"

	"ritm/internal/days"
)

func TestCalendar(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	r := days.Range{From: mustDay(t, "2026-08-01"), To: mustDay(t, "2026-08-31")}

	if list, err := s.Calendar(ctx, r); err != nil || len(list) != 0 {
		t.Fatalf("empty calendar %v %v", list, err)
	}
	if first, last, err := s.DataExtent(ctx); err != nil || first != nil || last != nil {
		t.Fatalf("empty extent %v %v %v", first, last, err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO daily_activity (day, steps) VALUES ('2026-07-31', 100), ('2026-08-02', 8412), ('2026-08-05', NULL), ('2026-09-01', 50)")
	workout := func(id int, at time.Time, offMin int) {
		exec(`INSERT INTO workouts (id, activity_type, source_name, start_ms, end_ms, tz_offset_min, import_id)
			VALUES (?, 'HKWorkoutActivityTypeRunning', 'Apple Watch', ?, ?, ?, 'x')`, id, at.UnixMilli(), at.Add(time.Hour).UnixMilli(), offMin)
	}
	workout(1, time.Date(2026, 8, 2, 7, 0, 0, 0, time.UTC), 0)
	workout(2, time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC), 0)
	exec("INSERT INTO workout_routes (workout_id, points) VALUES (2, x'00')")
	// 22:30 UTC on 9 Aug is 00:30 on 10 Aug at UTC+2: the local day counts.
	workout(3, time.Date(2026, 8, 9, 22, 30, 0, 0, time.UTC), 120)
	workout(4, time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC), 0)

	list, err := s.Calendar(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	steps := 8412.0
	want := []CalendarDay{
		{Day: "2026-08-02", Activity: true, Steps: &steps, Workouts: 2, Route: true},
		{Day: "2026-08-05", Activity: true},
		{Day: "2026-08-10", Workouts: 1},
	}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("calendar\n got %+v\nwant %+v", list, want)
	}

	first, last, err := s.DataExtent(ctx)
	if err != nil || first == nil || last == nil || *first != "2026-07-31" || *last != "2026-10-03" {
		t.Fatalf("extent %v %v %v", first, last, err)
	}
}

func mustDay(t *testing.T, s string) days.Day {
	t.Helper()
	d, err := days.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
