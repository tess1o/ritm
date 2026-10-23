package ingest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type workoutSink struct{ workouts []*Workout }

func (s *workoutSink) ExportDate(int64)   {}
func (s *workoutSink) Me(Me)              {}
func (s *workoutSink) Record(*Record)     {}
func (s *workoutSink) Workout(w *Workout) { s.workouts = append(s.workouts, w) }
func (s *workoutSink) Summary(*Summary)   {}

func parseString(t *testing.T, doc string) (*workoutSink, error) {
	t.Helper()
	h := &workoutSink{}
	return h, NewParser(strings.NewReader(doc), time.UTC, newTypeSet(nil), h).Run(nil)
}

// A single huge token (here an attribute value) fails the import instead of being buffered.
func TestParserTokenLimit(t *testing.T) {
	doc := `<HealthData><Record type="HKQuantityTypeIdentifierStepCount" sourceName="` +
		strings.Repeat("x", maxTokenBytes+1<<16) + `"/></HealthData>`
	_, err := parseString(t, doc)
	var pe *ParseError
	if !errors.As(err, &pe) || !errors.Is(err, errTokenTooLarge) {
		t.Fatalf("err = %v, want a ParseError for the token size", err)
	}
	// Many ordinary tokens add up to far more than the limit without tripping it.
	var b strings.Builder
	b.WriteString("<HealthData>")
	for b.Len() < 2*maxTokenBytes {
		b.WriteString(`<Correlation type="x"><Note text="` + strings.Repeat("y", 200) + `"/></Correlation>`)
	}
	b.WriteString("</HealthData>")
	if _, err := parseString(t, b.String()); err != nil {
		t.Fatalf("large file of small tokens: %v", err)
	}
}

// A workout keeps at most maxChildren events and maxRouteFiles route files.
func TestParserChildLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<HealthData><Workout workoutActivityType="HKWorkoutActivityTypeRunning" sourceName="Watch" ` +
		`startDate="2026-01-05 07:00:00 +0000" endDate="2026-01-05 08:00:00 +0000">`)
	for i := range maxChildren + 5 {
		fmt.Fprintf(&b, `<WorkoutEvent type="HKWorkoutEventTypeMarker" date="2026-01-05 07:%02d:00 +0000"/>`, i%60)
	}
	b.WriteString("<WorkoutRoute>")
	for i := range maxRouteFiles + 5 {
		fmt.Fprintf(&b, `<FileReference path="/workout-routes/route_%d.gpx"/>`, i)
	}
	b.WriteString("</WorkoutRoute></Workout></HealthData>")
	h, err := parseString(t, b.String())
	if err != nil || len(h.workouts) != 1 {
		t.Fatalf("parse: %v, %d workouts", err, len(h.workouts))
	}
	w := h.workouts[0]
	if len(w.Events) != maxChildren || len(w.RoutePaths) != maxRouteFiles {
		t.Fatalf("%d events, %d route files; want %d, %d", len(w.Events), len(w.RoutePaths), maxChildren, maxRouteFiles)
	}
}

func TestParseGPXLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("<gpx><trk><trkseg>")
	for range 10 {
		b.WriteString(`<trkpt lat="50.4" lon="30.5"><ele>100</ele><time>2026-01-05T07:00:00Z</time></trkpt>`)
	}
	b.WriteString("</trkseg></trk></gpx>")
	if pts, err := parseGPX(strings.NewReader(b.String()), 10); err != nil || len(pts) != 10 {
		t.Fatalf("at the limit: %d points, %v", len(pts), err)
	}
	if pts, err := parseGPX(strings.NewReader(b.String()), 4); !errors.Is(err, errTooManyPoints) || len(pts) != 4 {
		t.Fatalf("over the limit: %d points, %v", len(pts), err)
	}
	huge := `<gpx><trk><name>` + strings.Repeat("n", maxTokenBytes+1<<16) + `</name></trk></gpx>`
	if _, err := parseGPX(strings.NewReader(huge), 10); !errors.Is(err, errTokenTooLarge) {
		t.Fatalf("huge text: %v", err)
	}
}
