package query

import (
	"context"
	"database/sql"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
)

// HRPoint is a downsampled heart-rate bucket.
type HRPoint struct {
	TMS    int64   `json:"t_ms"`
	AvgBpm float64 `json:"avg_bpm"`
	MinBpm float64 `json:"min_bpm"`
	MaxBpm float64 `json:"max_bpm"`
}

type SleepSegment struct {
	Stage   string `json:"stage" enum:"in_bed,asleep,core,deep,rem,awake"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

type DayDetail struct {
	Day           string           `json:"day"`
	StartMS       int64            `json:"start_ms"`
	EndMS         int64            `json:"end_ms"`
	Activity      *DailyActivity   `json:"activity"`
	Heart         *DailyHeart      `json:"heart"`
	Sleep         *SleepNight      `json:"sleep"`
	StoodHours    []int            `json:"stood_hours"`
	HR            []HRPoint        `json:"hr"`
	Workouts      []WorkoutSummary `json:"workouts"`
	SleepSegments []SleepSegment   `json:"sleep_segments"`
}

// DayDetail returns the 24-hour timeline for one local day.
func (s *Service) DayDetail(ctx context.Context, d days.Day, hrBucketS int) (*DayDetail, error) {
	r := days.Range{From: d, To: d}
	from, to := s.dayBounds(r)
	dd := &DayDetail{Day: d.String(), StartMS: from, EndMS: to, StoodHours: []int{}, SleepSegments: []SleepSegment{}}
	if a, err := s.DailyActivity(ctx, r, true); err != nil {
		return nil, err
	} else if len(a) == 1 {
		dd.Activity = &a[0]
	}
	if h, err := s.DailyHeart(ctx, r); err != nil {
		return nil, err
	} else if len(h) == 1 {
		dd.Heart = &h[0]
	}
	if sl, err := s.Sleep(ctx, r); err != nil {
		return nil, err
	} else if len(sl) == 1 {
		dd.Sleep = &sl[0]
	}
	var err error
	if dd.HR, err = s.HRSeries(ctx, from, to, int64(hrBucketS)*1000); err != nil {
		return nil, err
	}

	rows, err := s.db.RO.QueryContext(ctx, `SELECT DISTINCT start_ms, tz_offset_min FROM records WHERE type = ?
		AND category_value = 'HKCategoryValueAppleStandHourStood' AND start_ms >= ? AND start_ms < ?`, ingest.TypeStandHour, from, to)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for rows.Next() {
		var ms int64
		var off int
		if err := rows.Scan(&ms, &off); err != nil {
			rows.Close()
			return nil, err
		}
		h := days.LocalHour(ms, off)
		if !seen[h] {
			seen[h] = true
			dd.StoodHours = append(dd.StoodHours, h)
		}
	}
	rows.Close()

	if dd.Workouts, _, err = s.Workouts(ctx, WorkoutFilter{Range: r}, 50); err != nil {
		return nil, err
	}

	// Sleep stages from the night ending this morning (and any nap) for the timeline.
	if dd.Sleep != nil && dd.Sleep.BedtimeMS != nil {
		if dd.SleepSegments, err = s.sleepSegments(ctx, *dd.Sleep.BedtimeMS, to); err != nil {
			return nil, err
		}
	}
	return dd, nil
}

// sleepSegments returns the sleep samples in [from, to) of one source: the highest-priority
// source that recorded sleep (not just time in bed), as daily_sleep does (SRC-2). Without that,
// iPhone and Watch stages would overlap on the timeline.
func (s *Service) sleepSegments(ctx context.Context, from, to int64) ([]SleepSegment, error) {
	rows, err := s.db.RO.QueryContext(ctx, `SELECT source_name, category_value, start_ms, end_ms FROM records WHERE type = ?
		AND end_ms > ? AND start_ms < ? ORDER BY start_ms`, ingest.TypeSleep, from, to)
	if err != nil {
		return nil, err
	}
	bySource := map[string][]SleepSegment{}
	asleep := map[string]bool{}
	for rows.Next() {
		var src string
		var v sql.NullString
		var seg SleepSegment
		if err := rows.Scan(&src, &v, &seg.StartMS, &seg.EndMS); err != nil {
			rows.Close()
			return nil, err
		}
		if seg.Stage = derive.SleepStage(v.String); seg.Stage == "" {
			seg.Stage = derive.StageAsleep
		}
		bySource[src] = append(bySource[src], seg)
		if seg.Stage != derive.StageInBed && seg.Stage != derive.StageAwake {
			asleep[src] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	for _, src := range res.Sources {
		if asleep[src.Name] {
			return bySource[src.Name], nil
		}
	}
	for src := range asleep { // a source not in the priority list
		return bySource[src], nil
	}
	return []SleepSegment{}, nil
}

// HRSeries buckets heart-rate samples in [from, to) into bucketMS-wide points.
func (s *Service) HRSeries(ctx context.Context, from, to, bucketMS int64) ([]HRPoint, error) {
	if bucketMS <= 0 {
		bucketMS = 300_000
	}
	rows, err := s.db.RO.QueryContext(ctx, `SELECT (start_ms - ?) / ? AS b, avg(value), min(value), max(value) FROM records
		WHERE type = ? AND start_ms >= ? AND start_ms < ? GROUP BY b ORDER BY b`, from, bucketMS, ingest.TypeHeartRate, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HRPoint{}
	for rows.Next() {
		var b int64
		var p HRPoint
		if err := rows.Scan(&b, &p.AvgBpm, &p.MinBpm, &p.MaxBpm); err != nil {
			return nil, err
		}
		p.TMS = from + b*bucketMS
		p.AvgBpm = mathx.Round(p.AvgBpm, 1)
		out = append(out, p)
	}
	return out, rows.Err()
}
