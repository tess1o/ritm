// Package period resolves period expressions used by the REST API and MCP tools (MCP-2).
package period

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ritm/internal/days"
)

// Names lists the accepted relative period names.
var Names = []string{"today", "yesterday", "last_7d", "last_14d", "last_30d", "last_90d", "last_365d",
	"this_week", "last_week", "this_month", "last_month", "ytd", "week", "month", "3months", "year"}

// Resolve turns an expression into an inclusive day range relative to today:
//   - relative names (see Names); "week", "month", "3months", "year" are the trailing 7/30/91/365 days
//   - "last_<n>d"
//   - "YYYY-MM-DD" (one day) or "YYYY-MM-DD..YYYY-MM-DD"
func Resolve(expr string, today days.Day) (days.Range, error) {
	e := strings.TrimSpace(strings.ToLower(expr))
	trailing := func(n int) days.Range { return days.Range{From: today.Add(-n + 1), To: today} }
	switch e {
	case "today":
		return days.Range{From: today, To: today}, nil
	case "yesterday":
		return days.Range{From: today - 1, To: today - 1}, nil
	case "week":
		return trailing(7), nil
	case "month":
		return trailing(30), nil
	case "3months":
		return trailing(91), nil
	case "year":
		return trailing(365), nil
	case "this_week":
		return days.Range{From: today.ISOWeekStart(), To: today}, nil
	case "last_week":
		s := today.ISOWeekStart() - 7
		return days.Range{From: s, To: s + 6}, nil
	case "this_month":
		return days.Range{From: today.MonthStart(), To: today}, nil
	case "last_month":
		end := today.MonthStart() - 1
		return days.Range{From: end.MonthStart(), To: end}, nil
	case "ytd":
		y := today.Time(time.UTC).Year()
		return days.Range{From: days.MustParse(fmt.Sprintf("%04d-01-01", y)), To: today}, nil
	}
	if strings.HasPrefix(e, "last_") && strings.HasSuffix(e, "d") {
		n, err := strconv.Atoi(e[5 : len(e)-1])
		if err == nil && n > 0 && n <= 3660 {
			return trailing(n), nil
		}
	}
	if a, b, ok := strings.Cut(e, ".."); ok {
		from, err := Day(a, today)
		if err != nil {
			return days.Range{}, err
		}
		to, err := Day(b, today)
		if err != nil {
			return days.Range{}, err
		}
		return ordered(from, to)
	}
	if d, err := days.Parse(e); err == nil {
		return days.Range{From: d, To: d}, nil
	}
	return days.Range{}, fmt.Errorf("unknown period %q (use YYYY-MM-DD, FROM..TO or one of %s)", expr, strings.Join(Names, ", "))
}

// Day resolves a single day: YYYY-MM-DD, "today" or "yesterday".
func Day(s string, today days.Day) (days.Day, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "today":
		return today, nil
	case "yesterday":
		return today - 1, nil
	}
	return days.Parse(strings.TrimSpace(s))
}

// FromTo resolves from/to parameters; each may be a day or a relative period name (its start for
// from, its end for to). Missing values default to the trailing defaultDays ending today.
func FromTo(from, to string, today days.Day, defaultDays int) (days.Range, error) {
	r := days.Range{From: today.Add(-defaultDays + 1), To: today}
	if from != "" {
		if d, err := Day(from, today); err == nil {
			r.From = d
		} else if p, err := Resolve(from, today); err == nil {
			r.From = p.From
			if to == "" {
				r.To = p.To
			}
		} else {
			return r, fmt.Errorf("from: %w", err)
		}
	}
	if to != "" {
		if d, err := Day(to, today); err == nil {
			r.To = d
		} else if p, err := Resolve(to, today); err == nil {
			r.To = p.To
		} else {
			return r, fmt.Errorf("to: %w", err)
		}
	}
	return ordered(r.From, r.To)
}

func ordered(a, b days.Day) (days.Range, error) {
	if b < a {
		return days.Range{}, fmt.Errorf("range end %s is before start %s", b, a)
	}
	if b-a > 3660 {
		return days.Range{}, fmt.Errorf("range longer than 10 years")
	}
	return days.Range{From: a, To: b}, nil
}
