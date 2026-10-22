package ingest

import (
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCleanReaderStripsDoctypeAndControlChars(t *testing.T) {
	in := "<?xml version=\"1.0\"?>\n<!DOCTYPE HealthData [\n<!ELEMENT HealthData (Record)*>\n<!ATTLIST Record a CDATA #REQUIRED a CDATA #REQUIRED>\n]>\n<HealthData><Record value=\"a\x01b\x0bc\"/></HealthData>\n"
	out, err := io.ReadAll(NewCleanReader(strings.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	want := "<?xml version=\"1.0\"?>\n\n<HealthData><Record value=\"abc\"/></HealthData>\n"
	if string(out) != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
}

func TestCleanReaderWithoutDoctype(t *testing.T) {
	in := "<HealthData>\t<Record/>\r\n</HealthData>"
	out, _ := io.ReadAll(NewCleanReader(strings.NewReader(in)))
	if string(out) != in {
		t.Fatalf("got %q", out)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in     string
		utc    string
		offMin int
	}{
		{"2023-10-02 18:01:12 +0300", "2023-10-02T15:01:12Z", 180},
		{"2023-03-26 02:30:00 +0200", "2023-03-26T00:30:00Z", 120},
		{"2023-01-01 00:00:00 -0530", "2023-01-01T05:30:00Z", -330},
	}
	for _, c := range cases {
		ms, off, ok := parseDate(c.in, time.UTC)
		if !ok {
			t.Fatalf("%s: not ok", c.in)
		}
		if got := time.UnixMilli(ms).UTC().Format(time.RFC3339); got != c.utc || off != c.offMin {
			t.Errorf("%s: got %s off %d, want %s off %d", c.in, got, off, c.utc, c.offMin)
		}
	}
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	ms, off, ok := parseDate("2023-07-01 12:00:00", kyiv)
	if !ok || off != 180 || time.UnixMilli(ms).UTC().Hour() != 9 {
		t.Errorf("fallback zone: ms=%d off=%d ok=%v", ms, off, ok)
	}
	for _, bad := range []string{"", "2023-13-01 00:00:00 +0000", "2023-10-02T18:01:12 +0300", "2023-10-02 18:01:12 +03x0"} {
		if _, _, ok := parseDate(bad, time.UTC); ok {
			t.Errorf("%q: expected failure", bad)
		}
	}
}

func TestLocalDay(t *testing.T) {
	ms, off, _ := parseDate("2023-10-02 00:30:00 +0300", time.UTC)
	if d := LocalDay(ms, off); d != "2023-10-02" {
		t.Fatalf("got %s", d)
	}
	if d := LocalDay(ms, 0); d != "2023-10-01" {
		t.Fatalf("got %s", d)
	}
	if d := DayString(-1); d != "1969-12-31" {
		t.Fatalf("got %s", d)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		typ, unit string
		in        float64
		wantUnit  string
		want      float64
	}{
		{TypeDistance, "km", 1.5, "m", 1500},
		{TypeDistance, "mi", 1, "m", 1609.344},
		{TypeActiveEnergy, "Cal", 100, "kcal", 100},
		{TypeActiveEnergy, "kJ", 418.4, "kcal", 100},
		{TypeExerciseTime, "min", 2, "s", 120},
		{TypeHeartRate, "count/min", 70, "bpm", 70},
		{TypeHRV, "ms", 45, "ms", 45},
		{TypeSpO2, "%", 0.97, "%", 97},
		{TypeBodyMass, "lb", 100, "kg", 45.359237},
		{TypeWristTemperature, "degF", 95, "degC", 35},
	}
	for _, c := range cases {
		u, v := normalize(c.typ, c.unit, c.in)
		if u != c.wantUnit || math.Abs(v-c.want) > 1e-9 {
			t.Errorf("%s %v %s: got %v %s, want %v %s", c.typ, c.in, c.unit, v, u, c.want, c.wantUnit)
		}
	}
}

func TestSplitHours(t *testing.T) {
	type piece struct {
		day  string
		hour int
		v    float64
	}
	collect := func(start, end string, v float64) []piece {
		s, off, _ := parseDate(start, time.UTC)
		e, _, _ := parseDate(end, time.UTC)
		var out []piece
		splitHours(s, e, off, v, func(hs int64, day string, hour int, v float64) {
			if LocalDay(hs, off) != day {
				t.Errorf("hour start %d not in day %s", hs, day)
			}
			out = append(out, piece{day, hour, v})
		})
		return out
	}
	got := collect("2023-10-02 09:50:00 +0300", "2023-10-02 10:20:00 +0300", 90)
	if len(got) != 2 || got[0] != (piece{"2023-10-02", 9, 30}) || got[1] != (piece{"2023-10-02", 10, 60}) {
		t.Fatalf("split: %+v", got)
	}
	got = collect("2023-10-02 23:30:00 +0300", "2023-10-03 00:30:00 +0300", 10)
	if len(got) != 2 || got[0].day != "2023-10-02" || got[1].day != "2023-10-03" || got[1].hour != 0 {
		t.Fatalf("midnight: %+v", got)
	}
	got = collect("2023-10-02 10:00:00 +0300", "2023-10-02 11:00:00 +0300", 5)
	if len(got) != 1 || got[0].hour != 10 {
		t.Fatalf("exact hour: %+v", got)
	}
	got = collect("2023-10-02 10:15:00 +0300", "2023-10-02 10:15:00 +0300", 5)
	if len(got) != 1 || got[0].v != 5 {
		t.Fatalf("instant: %+v", got)
	}
}
