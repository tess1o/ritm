package ingest

import (
	"time"

	"ritm/internal/days"
)

// parseDate parses Apple's "2006-01-02 15:04:05 -0700" into UTC epoch ms plus the original
// offset in minutes (PAR-5). Dates without an offset are interpreted in loc.
func parseDate(s string, loc *time.Location) (ms int64, offMin int, ok bool) {
	if len(s) < 19 || s[4] != '-' || s[7] != '-' || s[10] != ' ' || s[13] != ':' || s[16] != ':' {
		return 0, 0, false
	}
	y, ok1 := digits(s[0:4])
	mo, ok2 := digits(s[5:7])
	d, ok3 := digits(s[8:10])
	h, ok4 := digits(s[11:13])
	mi, ok5 := digits(s[14:16])
	sec, ok6 := digits(s[17:19])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) || mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 60 {
		return 0, 0, false
	}
	if len(s) >= 25 && s[19] == ' ' && (s[20] == '+' || s[20] == '-') {
		oh, okh := digits(s[21:23])
		om, okm := digits(s[23:25])
		if !okh || !okm {
			return 0, 0, false
		}
		offMin = oh*60 + om
		if s[20] == '-' {
			offMin = -offMin
		}
		t := time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC)
		return t.UnixMilli() - int64(offMin)*60_000, offMin, true
	}
	if len(s) != 19 {
		return 0, 0, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, 0, loc)
	_, offSec := t.Zone()
	return t.UnixMilli(), offSec / 60, true
}

func digits(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// LocalDay returns the YYYY-MM-DD local day of an instant given its offset.
func LocalDay(ms int64, offMin int) string { return days.FromMS(ms, offMin).String() }

// DayString formats days since the Unix epoch as YYYY-MM-DD.
func DayString(d int64) string { return days.Day(d).String() }
