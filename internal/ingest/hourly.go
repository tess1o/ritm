package ingest

import "ritm/internal/days"

// splitHours attributes a cumulative sample to local hour buckets using the sample's own offset,
// splitting proportionally to overlap when it spans several hours.
func splitHours(startMS, endMS int64, offMin int, value float64, fn func(hourStartMS int64, day string, hour int, v float64)) {
	off := int64(offMin) * 60_000
	ls, le := startMS+off, endMS+off
	hs := days.FloorDiv(ls, days.MSPerHour)
	if le <= ls {
		emitHour(hs, off, value, fn)
		return
	}
	he := days.FloorDiv(le-1, days.MSPerHour)
	if hs == he {
		emitHour(hs, off, value, fn)
		return
	}
	span := float64(le - ls)
	for h := hs; h <= he; h++ {
		a, b := max(ls, h*days.MSPerHour), min(le, (h+1)*days.MSPerHour)
		if b > a {
			emitHour(h, off, value*float64(b-a)/span, fn)
		}
	}
}

func emitHour(h, off int64, v float64, fn func(int64, string, int, float64)) {
	localStart := h * days.MSPerHour
	day := days.FloorDiv(localStart, days.MSPerDay)
	fn(localStart-off, DayString(day), int((localStart-day*days.MSPerDay)/days.MSPerHour), v)
}
