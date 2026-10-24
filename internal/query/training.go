package query

import (
	"context"
	"database/sql"
	"encoding/json"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

type LoadDay struct {
	Day      string   `json:"day"`
	Load     float64  `json:"load"`
	CTL      *float64 `json:"ctl"`
	ATL      *float64 `json:"atl"`
	TSB      *float64 `json:"tsb"`
	ACWR     *float64 `json:"acwr"`
	Sessions int      `json:"sessions"`
}

type TrainingLoad struct {
	LoadModel string `json:"load_model"`
	// SRPEScale (sRPE model only): unrated workouts count as Edwards TRIMP × factor.
	SRPEScale *derive.SRPEScale `json:"srpe_scale,omitempty"`
	Days      []LoadDay         `json:"days"`
	Latest    *LoadDay          `json:"latest"`
}

func (s *Service) TrainingLoad(ctx context.Context, r days.Range) (*TrainingLoad, error) {
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.RO.QueryContext(ctx, "SELECT day, load, ctl, atl, tsb, acwr, sessions FROM daily_load WHERE day BETWEEN ? AND ? ORDER BY day",
		r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &TrainingLoad{LoadModel: res.LoadModel, Days: []LoadDay{}}
	if res.LoadModel == "srpe" {
		sc, err := derive.LoadSRPEScale(ctx, s.db.RO)
		if err != nil {
			return nil, err
		}
		out.SRPEScale = &sc
	}
	for rows.Next() {
		var d LoadDay
		var ctl, atl, tsb, acwr sql.NullFloat64
		if err := rows.Scan(&d.Day, &d.Load, &ctl, &atl, &tsb, &acwr, &d.Sessions); err != nil {
			return nil, err
		}
		d.CTL, d.ATL, d.TSB, d.ACWR = nf(ctl), nf(atl), nf(tsb), nf(acwr)
		out.Days = append(out.Days, d)
	}
	if n := len(out.Days); n > 0 {
		out.Latest = &out.Days[n-1]
	}
	return out, nil
}

type WeekZones struct {
	WeekStart string     `json:"week_start"`
	ZoneS     [6]float64 `json:"zone_s"` // below, z1..z5
}

type SessionHR struct {
	ID        int64    `json:"id"`
	Day       string   `json:"day"`
	AvgHRBpm  *float64 `json:"avg_hr_bpm"`
	DurationS *float64 `json:"duration_s"`
	PctZ3Plus *float64 `json:"pct_z3_plus"`
}

// MonthVolume is the main-type training volume of one calendar month.
type MonthVolume struct {
	Month    string  `json:"month"` // YYYY-MM
	Sessions int     `json:"sessions"`
	Hours    float64 `json:"hours"`
}

// TrainingTotals summarises the main-type sessions of the period.
type TrainingTotals struct {
	Sessions       int      `json:"sessions"`
	Hours          float64  `json:"hours"`
	AvgHRBpm       *float64 `json:"avg_hr_bpm"`
	AvgDurationMin *float64 `json:"avg_duration_min"`
}

type Weekly struct {
	Target        int            `json:"target_sessions"`
	SessionTypes  []string       `json:"session_types"`
	SessionNames  []string       `json:"session_type_names"`
	SessionsLabel string         `json:"sessions_label"`
	Weeks         []derive.Week  `json:"weeks"`
	CurrentStreak int            `json:"current_streak_weeks"`
	LongestStreak int            `json:"longest_streak_weeks"`
	Zones         []WeekZones    `json:"zones_by_week"`
	Sessions      []SessionHR    `json:"sessions"`
	Monthly       []MonthVolume  `json:"monthly"`
	Totals        TrainingTotals `json:"totals"`
}

// TrainingWeekly returns consistency per ISO week, zone distribution and session HR trend.
func (s *Service) TrainingWeekly(ctx context.Context, r days.Range) (*Weekly, error) {
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	target := res.Goals.SessionsPerWeek
	weeks, err := derive.WeeklySessions(ctx, s.db.RO, r, target, res.SessionTypesEffective, s.Today())
	if err != nil {
		return nil, err
	}
	// Streaks consider the whole history.
	var first sql.NullString
	if err := s.db.RO.QueryRowContext(ctx, "SELECT min(day) FROM daily_load").Scan(&first); err != nil {
		return nil, err
	}
	out := &Weekly{Target: target, SessionTypes: res.SessionTypesEffective, SessionNames: res.SessionTypeNames, SessionsLabel: res.SessionsLabel, Weeks: weeks,
		Zones: []WeekZones{}, Sessions: []SessionHR{}, Monthly: []MonthVolume{}}
	if first.Valid {
		f, _ := days.Parse(first.String)
		all, err := derive.WeeklySessions(ctx, s.db.RO, days.Range{From: f, To: s.Today()}, target, res.SessionTypesEffective, s.Today())
		if err != nil {
			return nil, err
		}
		out.CurrentStreak, out.LongestStreak = derive.Streaks(all)
	}

	rows, err := s.db.RO.QueryContext(ctx, `SELECT id, local_day, avg_hr_bpm, duration_s, pct_z3_plus, zone_s_json FROM v_workouts
		WHERE activity_type IN (`+store.Placeholders(len(res.SessionTypesEffective))+`) AND local_day BETWEEN ? AND ? ORDER BY start_ms`,
		append(store.Anys(res.SessionTypesEffective), r.From.String(), r.To.String())...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byWeek := map[string]*WeekZones{}
	var hrSum float64
	var hrN int
	for rows.Next() {
		var sh SessionHR
		var avg, dur, pct sql.NullFloat64
		var zj sql.NullString
		if err := rows.Scan(&sh.ID, &sh.Day, &avg, &dur, &pct, &zj); err != nil {
			return nil, err
		}
		sh.AvgHRBpm, sh.DurationS, sh.PctZ3Plus = nf(avg), nf(dur), nf(pct)
		out.Sessions = append(out.Sessions, sh)
		month := sh.Day[:7]
		if n := len(out.Monthly); n == 0 || out.Monthly[n-1].Month != month {
			out.Monthly = append(out.Monthly, MonthVolume{Month: month})
		}
		mv := &out.Monthly[len(out.Monthly)-1]
		mv.Sessions++
		mv.Hours += dur.Float64 / 3600
		out.Totals.Sessions++
		out.Totals.Hours += dur.Float64 / 3600
		if avg.Valid {
			hrSum, hrN = hrSum+avg.Float64, hrN+1
		}
		var z derive.Zones
		if zj.Valid && json.Unmarshal([]byte(zj.String), &z) == nil {
			d, _ := days.Parse(sh.Day)
			ws := d.ISOWeekStart().String()
			wz := byWeek[ws]
			if wz == nil {
				wz = &WeekZones{WeekStart: ws}
				byWeek[ws] = wz
			}
			wz.ZoneS[0] += z.Below
			for i, v := range z.Z {
				wz.ZoneS[i+1] += v
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out.Monthly {
		out.Monthly[i].Hours = mathx.Round(out.Monthly[i].Hours, 2)
	}
	if n := out.Totals.Sessions; n > 0 {
		out.Totals.AvgDurationMin = mathx.RoundPtr(out.Totals.Hours*60/float64(n), 1)
	}
	if hrN > 0 {
		out.Totals.AvgHRBpm = mathx.RoundPtr(hrSum/float64(hrN), 1)
	}
	out.Totals.Hours = mathx.Round(out.Totals.Hours, 2)
	for _, w := range weeks {
		if wz, ok := byWeek[w.Start]; ok {
			out.Zones = append(out.Zones, *wz)
		} else {
			out.Zones = append(out.Zones, WeekZones{WeekStart: w.Start})
		}
	}
	return out, nil
}
