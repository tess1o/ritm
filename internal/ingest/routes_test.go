package ingest

import (
	"context"
	"errors"
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
