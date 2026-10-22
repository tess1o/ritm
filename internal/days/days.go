// Package days provides calendar-day arithmetic on local days represented as days since 1970-01-01.
package days

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Day is a local calendar day, counted in days since 1970-01-01.
type Day int64

// Milliseconds per hour and per (24-hour) day.
const (
	MSPerHour int64 = 60 * 60 * 1000
	MSPerDay  int64 = 24 * MSPerHour
)

// FromMS returns the local day of an instant with the given UTC offset in minutes.
func FromMS(ms int64, offMin int) Day {
	return Day(FloorDiv(ms+int64(offMin)*60_000, MSPerDay))
}

// LocalHour returns the local hour (0–23) of an instant with the given UTC offset in minutes.
func LocalHour(ms int64, offMin int) int {
	local := ms + int64(offMin)*60_000
	return int((local - FloorDiv(local, MSPerDay)*MSPerDay) / MSPerHour)
}

// FromTime returns the calendar day of t in its own location.
func FromTime(t time.Time) Day {
	y, m, d := t.Date()
	return Day(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func Parse(s string) (Day, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, fmt.Errorf("invalid day %q, want YYYY-MM-DD", s)
	}
	return Day(t.Unix() / 86400), nil
}

func MustParse(s string) Day {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Day) String() string {
	y, m, dd := civil(int64(d))
	b := [10]byte{
		byte('0' + y/1000%10), byte('0' + y/100%10), byte('0' + y/10%10), byte('0' + y%10), '-',
		byte('0' + m/10), byte('0' + m%10), '-',
		byte('0' + dd/10), byte('0' + dd%10),
	}
	return string(b[:])
}

func (d Day) Add(n int) Day { return d + Day(n) }

// Time returns local midnight of the day in loc.
func (d Day) Time(loc *time.Location) time.Time {
	y, m, dd := civil(int64(d))
	return time.Date(int(y), time.Month(m), int(dd), 0, 0, 0, 0, loc)
}

// StartMS is the UTC epoch ms of local midnight in loc.
func (d Day) StartMS(loc *time.Location) int64 { return d.Time(loc).UnixMilli() }

func (d Day) Weekday() time.Weekday { return time.Weekday((int64(d)%7 + 7 + 4) % 7) } // 1970-01-01 was a Thursday

// ISOWeekStart returns the Monday of the day's ISO week.
func (d Day) ISOWeekStart() Day {
	wd := int(d.Weekday())
	if wd == 0 {
		wd = 7
	}
	return d - Day(wd-1)
}

// MonthStart returns the first day of the day's month.
func (d Day) MonthStart() Day {
	y, m, _ := civil(int64(d))
	return FromTime(time.Date(int(y), time.Month(m), 1, 0, 0, 0, 0, time.UTC))
}

func (d Day) IsWeekend() bool {
	wd := d.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// Range is an inclusive span of days.
type Range struct {
	From, To Day
}

func (r Range) Len() int { return int(r.To-r.From) + 1 }

func (r Range) Contains(d Day) bool { return d >= r.From && d <= r.To }

func (r Range) String() string { return r.From.String() + ".." + r.To.String() }

// civil converts days since the epoch to a civil date (Howard Hinnant's algorithm).
func civil(z int64) (y, m, d int64) {
	z += 719468
	era := FloorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

// FloorDiv divides rounding towards negative infinity.
func FloorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Zone is a time zone that can change while services use it (a user's time-zone setting).
type Zone struct{ p atomic.Pointer[time.Location] }

func NewZone(loc *time.Location) *Zone {
	z := &Zone{}
	z.p.Store(loc)
	return z
}

func (z *Zone) Location() *time.Location { return z.p.Load() }

func (z *Zone) Set(loc *time.Location) { z.p.Store(loc) }

// Today is the calendar day of now in the zone.
func (z *Zone) Today(now time.Time) Day { return FromTime(now.In(z.Location())) }
