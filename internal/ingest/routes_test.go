package ingest

import (
	"context"
	"errors"
	"math"
	"testing"

	"ritm/internal/synth"
)

func TestRoutesStoredAndBackfilled(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	o := baseOpts(14)
	o.Walks = true // two Saturdays
	path := writeExport(t, "a.zip", o)

	if st := runImport(t, db, path, false); st.RoutesStored != 2 {
		t.Fatalf("routes stored = %d, want 2 (warnings %v)", st.RoutesStored, st.Warnings)
	}
	var blob []byte
	if err := db.RO.QueryRowContext(ctx, "SELECT points FROM workout_routes LIMIT 1").Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if pts, err := DecodeRoute(blob); err != nil || len(pts) != synth.WalkPoints {
		t.Fatalf("decoded %d points (%v), want %d", len(pts), err, synth.WalkPoints)
	}

	// Workouts imported before their routes were stored get them from a later upload of the same
	// export, although the workouts themselves are duplicates.
	if _, err := db.W.ExecContext(ctx, "DELETE FROM workout_routes"); err != nil {
		t.Fatal(err)
	}
	st := runImport(t, db, path, false)
	if st.Workouts.Inserted != 0 || st.RoutesStored != 2 {
		t.Fatalf("backfill: inserted %d workouts, stored %d routes", st.Workouts.Inserted, st.RoutesStored)
	}
	if st := runImport(t, db, path, false); st.RoutesStored != 0 {
		t.Fatalf("routes re-stored: %d", st.RoutesStored)
	}
}

func TestFailedRecomputeFailsImport(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	path := writeExport(t, "a.zip", baseOpts(3))
	var got []string
	post := func(_ context.Context, day string) error {
		got = append(got, day)
		return errors.New("boom")
	}
	_, err := RunRecorded(ctx, db, path, "a.zip", 0, Options{Loc: testLoc}, post)
	if err == nil {
		t.Fatal("expected the import to fail when the recompute fails")
	}
	var status string
	var n int
	db.RO.QueryRowContext(ctx, "SELECT status FROM imports").Scan(&status)
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&n)
	if status != StatusFailed || n == 0 {
		t.Fatalf("status %q with %d records; want failed with the data kept", status, n)
	}

	// The hook runs even when nothing new is stored, so it can retry a pending recompute.
	got = nil
	RunRecorded(ctx, db, path, "a.zip", 0, Options{Loc: testLoc, DeltaWindow: 0}, func(_ context.Context, day string) error {
		got = append(got, day)
		return nil
	})
	if len(got) != 1 {
		t.Fatalf("post-import hook calls: %v", got)
	}
}

// Stored routes carry their location summary, and routes stored before it existed get it filled in.
func TestRouteLocations(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	o := baseOpts(14)
	o.Walks = true
	runImport(t, db, writeExport(t, "a.zip", o), false)
	check := func(when string) {
		t.Helper()
		var n, points int
		var lat, lon, maxLat float64
		if err := db.RO.QueryRowContext(ctx, `SELECT count(*), min(point_count), min(start_lat), min(start_lon), max(max_lat)
			FROM workout_routes WHERE point_count IS NOT NULL`).Scan(&n, &points, &lat, &lon, &maxLat); err != nil {
			t.Fatal(err)
		}
		if n != 2 || points != synth.WalkPoints || math.Abs(lat-50.45) > 1e-6 || math.Abs(lon-30.52) > 1e-6 || maxLat <= lat {
			t.Fatalf("%s: %d located routes, %d points, start %v,%v, max lat %v", when, n, points, lat, lon, maxLat)
		}
	}
	check("after import")

	if _, err := db.W.ExecContext(ctx, "UPDATE workout_routes SET point_count = NULL, start_lat = NULL"); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	db.RO.QueryRowContext(ctx, "SELECT data_version FROM data_freshness").Scan(&before)
	if n, err := BackfillRouteLocations(ctx, db); err != nil || n != 2 {
		t.Fatalf("backfill: %d, %v", n, err)
	}
	check("after backfill")
	db.RO.QueryRowContext(ctx, "SELECT data_version FROM data_freshness").Scan(&after)
	if after <= before {
		t.Fatalf("data version %d → %d, want it to go up", before, after)
	}
	if n, err := BackfillRouteLocations(ctx, db); err != nil || n != 0 {
		t.Fatalf("second backfill: %d, %v", n, err)
	}
}

func TestWorkoutCategory(t *testing.T) {
	for typ, want := range map[string]string{
		"HKWorkoutActivityTypeRunning": "run", "HKWorkoutActivityTypeWalking": "walk",
		"HKWorkoutActivityTypeFunctionalStrengthTraining": "strength", "HKWorkoutActivityTypeYoga": "other",
	} {
		if got, _ := WorkoutCategory(typ); got != want {
			t.Errorf("%s: %s, want %s", typ, got, want)
		}
	}
	if _, d := WorkoutCategory("HKWorkoutActivityTypeCycling"); !d {
		t.Error("cycling should be distance based")
	}
	for _, c := range Categories[:len(Categories)-1] {
		if len(CategoryTypes(c)) == 0 {
			t.Errorf("category %s has no types", c)
		}
	}
}
