package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"ritm/internal/days"
	"ritm/internal/mathx"
)

// Tool results are compact JSON for a language model: epoch-millisecond fields become
// local "YYYY-MM-DD HH:MM" strings without the _ms suffix, numbers are rounded to 2 decimals and
// null fields are dropped (a missing field means no data).

type row = map[string]any

// toAny round-trips v through JSON into maps, slices and float64s.
func toAny(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// toRows converts a slice of structs into rows, flattening nested objects ("rings": {"move_kcal"})
// into prefixed keys ("rings_move_kcal").
func toRows(v any) ([]row, error) {
	a, err := toAny(v)
	if err != nil {
		return nil, err
	}
	list, _ := a.([]any)
	out := make([]row, 0, len(list))
	for _, it := range list {
		m, ok := it.(row)
		if !ok {
			continue
		}
		flat := row{}
		for k, v := range m {
			if sub, ok := v.(row); ok {
				for sk, sv := range sub {
					flat[k+"_"+sk] = sv
				}
				continue
			}
			flat[k] = v
		}
		out = append(out, flat)
	}
	return out, nil
}

// epochMS: numbers at least this large in *_ms fields are timestamps, not durations.
const epochMS = 1e11

// shape makes a decoded JSON value model-friendly (see the package comment).
func shape(v any, loc *time.Location) any {
	switch x := v.(type) {
	case row:
		out := row{}
		for k, val := range x {
			if val == nil {
				continue
			}
			if base, ok := strings.CutSuffix(k, "_ms"); ok && hasEpoch(val) {
				out[base] = localize(val, loc)
				continue
			}
			out[k] = shape(val, loc)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = shape(e, loc)
		}
		return out
	case float64:
		return mathx.Round(x, 2)
	}
	return v
}

func hasEpoch(v any) bool {
	switch x := v.(type) {
	case float64:
		return x >= epochMS
	case []any:
		for _, e := range x {
			if hasEpoch(e) {
				return true
			}
		}
	}
	return false
}

func localize(v any, loc *time.Location) any {
	switch x := v.(type) {
	case float64:
		if x >= epochMS {
			return time.UnixMilli(int64(x)).In(loc).Format("2006-01-02 15:04")
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = localize(e, loc)
		}
		return out
	}
	return v
}

// Granularities of time series. "auto" picks day up to 3 months, week up to 2 years, else month.
const (
	granAuto  = "auto"
	granDay   = "day"
	granWeek  = "week"
	granMonth = "month"
)

func resolveGranularity(g string, r days.Range) (string, error) {
	switch g {
	case "", granAuto:
		switch n := r.Len(); {
		case n <= 92:
			return granDay, nil
		case n <= 730:
			return granWeek, nil
		default:
			return granMonth, nil
		}
	case granDay, granWeek, granMonth:
		return g, nil
	}
	return "", fmt.Errorf("granularity must be auto, day, week or month")
}

// aggregate groups daily rows (with a "day" key) into weeks or months. Numeric fields become the
// mean over days that have a value, booleans become "<name>_days" counts, and "days" counts the
// rows. Timestamps and text are dropped. Rows keep "period" (ISO week start or YYYY-MM).
func aggregate(rows []row, gran string) []row {
	if gran == granDay {
		return rows
	}
	type acc struct {
		sum   map[string]float64
		n     map[string]int
		trues map[string]int
		days  int
	}
	groups := map[string]*acc{}
	var keys []string
	for _, r := range rows {
		ds, _ := r["day"].(string)
		d, err := days.Parse(ds)
		if err != nil {
			continue
		}
		key := d.ISOWeekStart().String()
		if gran == granMonth {
			key = d.MonthStart().String()[:7]
		}
		a, ok := groups[key]
		if !ok {
			a = &acc{sum: map[string]float64{}, n: map[string]int{}, trues: map[string]int{}}
			groups[key] = a
			keys = append(keys, key)
		}
		a.days++
		for k, v := range r {
			if strings.HasSuffix(k, "_ms") {
				continue
			}
			switch x := v.(type) {
			case float64:
				a.sum[k] += x
				a.n[k]++
			case bool:
				if x {
					a.trues[k+"_days"]++
				} else if _, ok := a.trues[k+"_days"]; !ok {
					a.trues[k+"_days"] = 0
				}
			}
		}
	}
	sort.Strings(keys)
	out := make([]row, 0, len(keys))
	for _, k := range keys {
		a := groups[k]
		r := row{"period": k, "days": a.days}
		for f, s := range a.sum {
			r[f] = s / float64(a.n[f])
		}
		for f, n := range a.trues {
			r[f] = n
		}
		out = append(out, r)
	}
	return out
}

// mean averages the numeric fields of daily rows into one row, like a single aggregate bucket
// (used by compare_periods).
func mean(rows []row) row {
	sum, n, trues := map[string]float64{}, map[string]int{}, map[string]int{}
	for _, r := range rows {
		for k, v := range r {
			if strings.HasSuffix(k, "_ms") {
				continue
			}
			switch x := v.(type) {
			case float64:
				sum[k] += x
				n[k]++
			case bool:
				if x {
					trues[k+"_days"]++
				}
			}
		}
	}
	out := row{"days": len(rows)}
	for k, s := range sum {
		out[k] = s / float64(n[k])
	}
	for k, c := range trues {
		out[k] = c
	}
	return out
}

// capRows keeps the most recent limit rows and reports whether any were dropped.
func capRows(rows []row, limit int) ([]row, bool) {
	if len(rows) <= limit {
		return rows, false
	}
	return rows[len(rows)-limit:], true
}
