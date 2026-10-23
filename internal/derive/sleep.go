package derive

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
)

type interval struct{ start, end int64 }

type sleepSample struct {
	interval
	off   int
	stage string // one of the Stage constants
}

// sessionGapMS splits sleep samples into separate sessions.
const sessionGapMS = 2 * 3600 * 1000

// Sleep stages, as returned by SleepStage and served by the API.
const (
	StageInBed  = "in_bed"
	StageAsleep = "asleep" // asleep without stage detail
	StageCore   = "core"
	StageDeep   = "deep"
	StageREM    = "rem"
	StageAwake  = "awake"
)

// SleepStage maps a SleepAnalysis category value to a stage ("" for unknown values).
func SleepStage(v string) string {
	switch strings.TrimPrefix(v, "HKCategoryValueSleepAnalysis") {
	case "InBed":
		return StageInBed
	case "AsleepCore":
		return StageCore
	case "AsleepDeep":
		return StageDeep
	case "AsleepREM":
		return StageREM
	case "Awake":
		return StageAwake
	case "Asleep", "AsleepUnspecified":
		return StageAsleep
	}
	return ""
}

type night struct {
	inBed, asleep, core, deep, rem, awake float64 // minutes
	hasStages                             bool
	bed, wake                             int64
	asleepIntervals                       []interval
}

// computeSleep fills daily_sleep, assigning each night to its wake-up day, and returns
// the asleep intervals per day for the sleeping-HR average.
func (e *Engine) computeSleep(ctx context.Context, tx *sql.Tx, r days.Range, res *Resolved) (map[days.Day][]interval, error) {
	fromMS, toMS := r.From.StartMS(e.zone.Location())-msDay, r.To.Add(1).StartMS(e.zone.Location())+msDay/2
	rows, err := tx.QueryContext(ctx, `SELECT source_name, start_ms, end_ms, tz_offset_min, category_value FROM records
		WHERE type = ? AND start_ms >= ? AND end_ms >= ? AND start_ms < ? ORDER BY source_name, start_ms`,
		ingest.TypeSleep, fromMS-ingest.MaxSleepSampleMS, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	bySource := map[string][]sleepSample{}
	for rows.Next() {
		var src string
		var s sleepSample
		var v sql.NullString
		if err := rows.Scan(&src, &s.start, &s.end, &s.off, &v); err != nil {
			rows.Close()
			return nil, err
		}
		if s.stage = SleepStage(v.String); s.stage != "" && s.end > s.start {
			bySource[src] = append(bySource[src], s)
		}
	}
	rows.Close()

	rank := res.rank()
	type pick struct {
		rank int
		n    night
	}
	nights := map[days.Day]pick{}
	for src, samples := range bySource {
		for d, n := range buildNights(samples) {
			if !r.Contains(d) || n.asleep == 0 {
				continue
			}
			rk := rank(src)
			if p, ok := nights[d]; !ok || rk < p.rank {
				nights[d] = pick{rk, n}
			}
		}
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM daily_sleep WHERE day BETWEEN ? AND ?", r.From.String(), r.To.String()); err != nil {
		return nil, err
	}
	asleep := map[days.Day][]interval{}
	for d, p := range nights {
		n := p.n
		stage := func(v float64) any {
			if !n.hasStages {
				return nil
			}
			return mathx.Round(v, 1)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO daily_sleep (day, in_bed_min, asleep_min, core_min, deep_min, rem_min, awake_min, bedtime_ms, wake_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.String(), mathx.Round(n.inBed, 1), mathx.Round(n.asleep, 1),
			stage(n.core), stage(n.deep), stage(n.rem), mathx.Round(n.awake, 1), n.bed, n.wake); err != nil {
			return nil, err
		}
		asleep[d] = n.asleepIntervals
	}
	return asleep, nil
}

// buildNights clusters one source's samples into sessions and sums them per wake-up day.
func buildNights(samples []sleepSample) map[days.Day]night {
	sort.Slice(samples, func(i, j int) bool { return samples[i].start < samples[j].start })
	out := map[days.Day]night{}
	var session []sleepSample
	var sessEnd int64
	flush := func() {
		if len(session) == 0 {
			return
		}
		last := session[0]
		for _, s := range session {
			if s.end > last.end {
				last = s
			}
		}
		d := days.FromMS(last.end, last.off)
		n := out[d]
		var inBed []interval
		var span interval = interval{session[0].start, last.end}
		for _, s := range session {
			mins := float64(s.end-s.start) / 60000
			switch s.stage {
			case StageInBed:
				inBed = append(inBed, s.interval)
			case StageAwake:
				n.awake += mins
			default:
				n.asleep += mins
				n.asleepIntervals = append(n.asleepIntervals, s.interval)
				switch s.stage {
				case StageCore:
					n.core += mins
					n.hasStages = true
				case StageDeep:
					n.deep += mins
					n.hasStages = true
				case StageREM:
					n.rem += mins
					n.hasStages = true
				}
			}
		}
		if len(inBed) > 0 {
			n.inBed += unionMinutes(inBed)
		} else {
			n.inBed += float64(span.end-span.start) / 60000
		}
		// bedtime/wake time come from the longest session of the day
		if span.end-span.start > n.wake-n.bed {
			n.bed, n.wake = span.start, span.end
		}
		out[d] = n
		session = session[:0]
	}
	for _, s := range samples {
		if len(session) > 0 && s.start-sessEnd > sessionGapMS {
			flush()
		}
		if len(session) == 0 || s.end > sessEnd {
			sessEnd = s.end
		}
		session = append(session, s)
	}
	flush()
	return out
}

func unionMinutes(iv []interval) float64 {
	sort.Slice(iv, func(i, j int) bool { return iv[i].start < iv[j].start })
	var total int64
	cur := iv[0]
	for _, x := range iv[1:] {
		if x.start <= cur.end {
			cur.end = max(cur.end, x.end)
			continue
		}
		total += cur.end - cur.start
		cur = x
	}
	total += cur.end - cur.start
	return float64(total) / 60000
}
