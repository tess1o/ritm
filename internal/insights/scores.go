package insights

import (
	"math"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/mathx"
)

// Score is a 0–100 display-only index with an explanation of its inputs.
type Score struct {
	Value  *float64       `json:"value"`
	Inputs map[string]any `json:"inputs"`
	Note   string         `json:"note"`
}

type Scores struct {
	Activity Score `json:"activity"`
	Heart    Score `json:"heart"`
	Training Score `json:"training"`
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ComputeScores scores the 7 days ending at end against goals and the 90-day baseline.
func ComputeScores(data *Data, res *derive.Resolved, end days.Day) Scores {
	week := days.Range{From: end.Add(-6), To: end}
	base := days.Range{From: end.Add(-96), To: end.Add(-7)}
	var s Scores

	steps, n := data.mean(mSteps, week)
	kcal, _ := data.mean(mKcal, week)
	kcalBase := mathx.Median(data.values(mKcal, base, nil))
	stand, _ := data.mean(func(d *Day) float64 { return watchOnly(d, d.StandHours) }, week)
	if n >= 3 {
		parts := []float64{clamp01(steps / res.Goals.StepsPerDay)}
		if !math.IsNaN(kcalBase) && kcalBase > 0 && !math.IsNaN(kcal) {
			parts = append(parts, clamp01(kcal/kcalBase))
		}
		if !math.IsNaN(stand) {
			parts = append(parts, clamp01(stand/res.Goals.StandHours))
		}
		s.Activity.Value = fp(math.Round(mathx.Mean(parts) * 100))
	}
	s.Activity.Inputs = map[string]any{"steps_per_day": fp(steps), "step_goal": res.Goals.StepsPerDay,
		"active_kcal_per_day": fp(kcal), "active_kcal_90d_median": fp(kcalBase), "stand_hours": fp(stand), "stand_goal": res.Goals.StandHours}
	s.Activity.Note = "Steps vs goal, active calories vs your 90-day median and stand hours vs goal, averaged over the last 7 days."

	rhr, rn := data.mean(mRHR, week)
	rhrBase := mathx.Median(data.values(mRHR, base, nil))
	hrv, hn := data.mean(mHRV, week)
	hrvBase := mathx.Median(data.values(mHRV, base, nil))
	if rn >= 3 && !math.IsNaN(rhrBase) {
		v := 50 + (rhrBase-rhr)/3*25 // 3 bpm below baseline → +25
		if hn >= 3 && !math.IsNaN(hrvBase) && hrvBase > 0 {
			v += (hrv - hrvBase) / hrvBase / 0.15 * 25 // 15 % above baseline → +25
		}
		s.Heart.Value = fp(math.Round(math.Max(0, math.Min(100, v))))
	}
	s.Heart.Inputs = map[string]any{"resting_hr_7d": fp(rhr), "resting_hr_90d_median": fp(rhrBase), "hrv_7d": fp(hrv), "hrv_90d_median": fp(hrvBase)}
	s.Heart.Note = "50 = at your 90-day baseline; lower resting HR and higher HRV raise the score."

	target := float64(res.Goals.SessionsPerWeek)
	sessions := float64(len(data.workoutsIn(days.Range{From: end.Add(-27), To: end})))
	var acwr float64 = math.NaN()
	if d, ok := data.Days[end]; ok {
		acwr = d.ACWR
	}
	if len(data.Workouts) > 0 {
		v := clamp01(sessions/(4*target)) * 60
		switch {
		case math.IsNaN(acwr):
			v += 20
		case acwr >= 0.8 && acwr <= 1.3:
			v += 40
		case acwr < 0.8:
			v += 40 * clamp01(acwr/0.8)
		default:
			v += 40 * clamp01(1-(acwr-1.3)/0.7)
		}
		s.Training.Value = fp(math.Round(v))
	}
	s.Training.Inputs = map[string]any{"sessions_28d": sessions, "target_28d": 4 * target, "acwr": fp(acwr)}
	s.Training.Note = "Consistency over 4 weeks (60 pts) plus load in the 0.8–1.3 ACWR range (40 pts)."
	return s
}
