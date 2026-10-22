package days

import (
	"testing"
	"time"
)

func TestDayBasics(t *testing.T) {
	d := MustParse("2026-10-22")
	if d.String() != "2026-10-22" || d.Weekday() != time.Thursday {
		t.Fatalf("%s %s", d, d.Weekday())
	}
	if d.ISOWeekStart().String() != "2026-10-19" {
		t.Fatalf("week start %s", d.ISOWeekStart())
	}
	if MustParse("2026-10-25").ISOWeekStart().String() != "2026-10-19" {
		t.Fatal("sunday week start")
	}
	if d.MonthStart().String() != "2026-10-01" {
		t.Fatal("month start")
	}
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	if got := FromMS(d.StartMS(kyiv), 180); got != d {
		t.Fatalf("roundtrip %s", got)
	}
	if Day(-1).String() != "1969-12-31" {
		t.Fatal("negative")
	}
}

func TestLocalHour(t *testing.T) {
	ms := time.Date(2026, 10, 22, 21, 30, 0, 0, time.UTC).UnixMilli()
	if h := LocalHour(ms, 180); h != 0 { // 00:30 next day at UTC+3
		t.Fatalf("UTC+3: %d", h)
	}
	if h := LocalHour(ms, -300); h != 16 {
		t.Fatalf("UTC-5: %d", h)
	}
	if h := LocalHour(-1, 0); h != 23 { // just before the epoch
		t.Fatalf("negative: %d", h)
	}
}
