package ingest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ritm/internal/store"
	"ritm/internal/synth"
)

var testLoc = time.FixedZone("EET", 2*3600)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.OpenUser(context.Background(), t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeExport(t *testing.T, name string, o synth.Options) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := synth.WriteZip(p, o); err != nil {
		t.Fatal(err)
	}
	return p
}

func runImport(t *testing.T, db *store.DB, path string, full bool) *ImportStats {
	t.Helper()
	st, err := RunRecorded(context.Background(), db, path, filepath.Base(path), 0,
		Options{Full: full, DeltaWindow: 7 * 24 * time.Hour, Loc: testLoc}, nil)
	if err != nil {
		t.Fatalf("import %s: %v", path, err)
	}
	return st
}

func baseOpts(days int) synth.Options {
	return synth.Options{Days: days, Loc: testLoc, Seed: 7, Sleep: true, IPhone: true, HRIntervalSec: 600, WorkoutHRIntervalSec: 30}
}

type snapshot struct {
	records, workouts, events, summaries int
	hourly                               map[string]float64
}

func snap(t *testing.T, db *store.DB) snapshot {
	t.Helper()
	ctx := context.Background()
	s := snapshot{hourly: map[string]float64{}}
	for q, dst := range map[string]*int{
		"SELECT count(*) FROM records":            &s.records,
		"SELECT count(*) FROM workouts":           &s.workouts,
		"SELECT count(*) FROM workout_events":     &s.events,
		"SELECT count(*) FROM activity_summaries": &s.summaries,
	} {
		if err := db.RO.QueryRowContext(ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.RO.QueryContext(ctx, "SELECT type || '|' || source_name || '|' || local_day, round(sum(value), 6) FROM hourly_activity GROUP BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v float64
		rows.Scan(&k, &v)
		s.hourly[k] = v
	}
	return s
}

func equalSnap(t *testing.T, label string, a, b snapshot) {
	t.Helper()
	if a.records != b.records || a.workouts != b.workouts || a.events != b.events || a.summaries != b.summaries {
		t.Errorf("%s: counts differ: %+v vs %+v", label, [4]int{a.records, a.workouts, a.events, a.summaries}, [4]int{b.records, b.workouts, b.events, b.summaries})
	}
	if len(a.hourly) != len(b.hourly) {
		t.Errorf("%s: hourly day-keys %d vs %d", label, len(a.hourly), len(b.hourly))
	}
	for k, v := range a.hourly {
		if bv, ok := b.hourly[k]; !ok || bv != v {
			t.Errorf("%s: hourly %s = %v vs %v", label, k, v, bv)
		}
	}
}

func TestImportParsesSyntheticExport(t *testing.T) {
	db := openDB(t)
	o := baseOpts(14)
	o.ControlChars = true
	o.Folder = "експорт_apple_health" // localised folder name
	st := runImport(t, db, writeExport(t, "export.zip", o), false)

	if st.Types[TypeHeartRate].Inserted == 0 || st.Types[TypeSteps].Inserted == 0 {
		t.Fatalf("expected HR and steps: %+v", st.Types)
	}
	if st.Workouts.Inserted != 6 { // Mon/Wed/Fri over two weeks
		t.Fatalf("workouts inserted = %d", st.Workouts.Inserted)
	}
	if st.SummariesChanged != 14 || st.ExportDateMS == 0 || st.EarliestDay != "2026-01-05" {
		t.Fatalf("summaries=%d export=%d earliest=%s", st.SummariesChanged, st.ExportDateMS, st.EarliestDay)
	}
	ctx := context.Background()
	var kind string
	if err := db.RO.QueryRowContext(ctx, "SELECT kind FROM sources WHERE name = 'iPhone Oleksandr'").Scan(&kind); err != nil || kind != "phone" {
		t.Fatalf("iphone kind %q err %v", kind, err)
	}
	var spo2 float64
	db.RO.QueryRowContext(ctx, "SELECT max(value) FROM records WHERE type = ?", TypeSpO2).Scan(&spo2)
	if spo2 < 90 || spo2 > 100 {
		t.Fatalf("SpO2 not normalised to percent: %v", spo2)
	}
	var stood int
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE type = ? AND category_value = 'HKCategoryValueAppleStandHourStood'", TypeStandHour).Scan(&stood)
	if stood == 0 {
		t.Fatal("no stand hour category records")
	}
	var indoor, pauses int
	var energy, duration float64
	db.RO.QueryRowContext(ctx, "SELECT indoor, energy_kcal, duration_s FROM workouts LIMIT 1").Scan(&indoor, &energy, &duration)
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM workout_events WHERE type = 'HKWorkoutEventTypePause'").Scan(&pauses)
	if indoor != 1 || energy != 402.5 || duration != 3600 || pauses != 6 {
		t.Fatalf("workout: indoor=%d energy=%v duration=%v pauses=%d", indoor, energy, duration, pauses)
	}
	// Watch steps for one day: 90 ten-minute samples between 08:00 and 23:00 land in 15 hours.
	var hours int
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM hourly_activity WHERE type = ? AND source_name = 'Apple Watch Oleksandr' AND local_day = '2026-01-06'", TypeSteps).Scan(&hours)
	if hours != 15 {
		t.Fatalf("watch step hours = %d", hours)
	}
	var dob string
	if ok, _ := store.GetSetting(ctx, db.RO, "me.dob", &dob); !ok || dob != "1986-04-12" {
		t.Fatalf("dob %q", dob)
	}
	var last int64
	db.RO.QueryRowContext(ctx, "SELECT last_data_at FROM data_freshness").Scan(&last)
	if last == 0 {
		t.Fatal("freshness not updated")
	}
}

func TestReimportSameExportInsertsNothing(t *testing.T) {
	db := openDB(t)
	path := writeExport(t, "a.zip", baseOpts(14))
	runImport(t, db, path, false)
	before := snap(t, db)
	st := runImport(t, db, path, false)
	for typ, ts := range st.Types {
		if !isCumulative(typ) && ts.Inserted != 0 {
			t.Errorf("%s inserted %d on re-import", typ, ts.Inserted)
		}
	}
	if st.Workouts.Inserted != 0 || st.SummariesChanged != 0 {
		t.Errorf("workouts %d summaries %d on re-import", st.Workouts.Inserted, st.SummariesChanged)
	}
	if st.Types[TypeHeartRate].BelowCutoff == 0 {
		t.Error("expected HWM to skip old heart-rate samples")
	}
	equalSnap(t, "A,A", before, snap(t, db))

	// A full re-import ignores HWM but still changes nothing.
	st = runImport(t, db, path, true)
	if st.Types[TypeHeartRate].BelowCutoff != 0 || st.Types[TypeHeartRate].Duplicate == 0 {
		t.Errorf("full re-import: %+v", st.Types[TypeHeartRate])
	}
	equalSnap(t, "A,A full", before, snap(t, db))
}

func isCumulative(t string) bool {
	for _, c := range CumulativeTypes {
		if c == t {
			return true
		}
	}
	return false
}

func TestImportOrderIndependence(t *testing.T) {
	a := writeExport(t, "a.zip", baseOpts(14))
	b := writeExport(t, "b.zip", baseOpts(21))

	dbAB := openDB(t)
	runImport(t, dbAB, a, false)
	st := runImport(t, dbAB, b, false)
	if st.Workouts.Inserted != 3 {
		t.Errorf("A then B: new workouts = %d, want 3", st.Workouts.Inserted)
	}
	if st.EarliestDay == "" || st.EarliestDay > "2026-01-12" {
		t.Errorf("earliest affected day %q", st.EarliestDay)
	}

	dbB := openDB(t)
	runImport(t, dbB, b, false)
	onlyB := snap(t, dbB)
	equalSnap(t, "A,B vs B", snap(t, dbAB), onlyB)

	st = runImport(t, dbB, a, false)
	if !st.OlderExport || len(st.Warnings) == 0 {
		t.Error("expected older-export warning")
	}
	equalSnap(t, "B,A vs B", snap(t, dbB), onlyB)
}

func TestBareXMLAndParseErrors(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "export.xml")
	f, _ := createFile(p)
	synth.WriteXML(f, baseOpts(2))
	f.Close()
	if st := runImport(t, db, p, false); st.Types[TypeHeartRate].Inserted == 0 {
		t.Fatal("bare xml: no heart rate")
	}

	bad := filepath.Join(dir, "bad.xml")
	writeString(bad, `<?xml version="1.0"?><HealthData><Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" startDate="2026-01-01 10:00:00 +0200" endDate="2026-01-01 10:00:00 +0200" value="x"/><Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" startDate="2026-01-01 10:00:00 +0200" endDate="2026-01-01 10:00:00 +0200" value="70"/><Broken></HealthData>`)
	_, err := RunRecorded(context.Background(), db, bad, "bad.xml", 0, Options{Loc: testLoc}, nil)
	if err == nil {
		t.Fatal("expected XML syntax error")
	}
	var status, msg string
	db.RO.QueryRowContext(context.Background(), "SELECT status, error FROM imports WHERE file_name = 'bad.xml'").Scan(&status, &msg)
	if status != StatusFailed || msg == "" {
		t.Fatalf("status %q msg %q", status, msg)
	}

	skip := filepath.Join(dir, "skip.xml")
	writeString(skip, `<?xml version="1.0"?><HealthData><Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" startDate="2026-01-01 10:00:00 +0200" endDate="2026-01-01 10:00:00 +0200" value="x"/><Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" startDate="2026-02-01 10:00:00 +0200" endDate="2026-02-01 10:00:00 +0200" value="70"/></HealthData>`)
	st := runImport(t, db, skip, true)
	if hr := st.Types[TypeHeartRate]; hr.Skipped != 1 || hr.Inserted != 1 {
		t.Fatalf("skip stats %+v", hr)
	}
}
