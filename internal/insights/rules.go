package insights

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/mathx"
)

// Noise-control parameters.
const (
	kStdev           = 1.0 // |Δ| must also exceed k × stdev of baseline weekly values
	minWatchDaysWeek = 5   // days with Watch data needed in a week
	minBaselineWeeks = 3   // of the 4 baseline weeks
)

// Rules returns the v1 rule set.
func Rules() []Rule {
	return []Rule{
		{"activity.steps_trend", "activity", Weekly, stepsTrend},
		{"activity.steps_trend_28d", "activity", Monthly, monthlyTrend("steps", mSteps, 0.10, 300, "steps/day", 0, true, "Steps")},
		{"activity.distance_trend", "activity", Monthly, monthlyTrend("distance_m", mDistance, 0.10, 250, "km/day", 3, true, "Walking distance")},
		{"activity.active_kcal_trend", "activity", Weekly, kcalTrend},
		{"activity.goal_attainment", "activity", Weekly, goalAttainment},
		{"activity.sedentary", "activity", Monthly, sedentary},
		{"activity.weekday_weekend", "activity", Monthly, weekdayWeekend},
		{"activity.gym_vs_rest_day", "activity", Monthly, gymVsRest},
		{"activity.rings", "activity", Weekly, rings},

		{"heart.resting_hr_trend", "heart", Monthly, bpmTrend("resting_hr", mRHR, "Resting HR", 2)},
		{"heart.resting_hr_spike", "heart", Daily, restingSpike},
		{"heart.hrv_trend", "heart", Weekly, hrvTrend},
		{"heart.walking_hr_trend", "heart", Monthly, bpmTrend("walking_hr", mWHR, "Walking HR average", 2)},
		{"heart.vo2max_change", "heart", LongTerm, vo2Change},
		{"heart.events", "heart", Monthly, heartEvents},

		{"training.consistency", "training", Weekly, consistency},
		{"training.monthly_volume", "training", Monthly, monthlyVolume},
		{"training.intensity", "training", Monthly, intensity},
		{"training.session_hr", "training", Monthly, sessionHR},
		{"training.load_form", "training", Weekly, loadForm},
		{"training.duration_drift", "training", Monthly, durationDrift},

		{"recovery.next_day", "recovery", LongTerm, nextDay},
		{"recovery.sleep", "recovery", Weekly, sleepTrend},
		{"recovery.sleep_hrv", "recovery", LongTerm, sleepHRV},
	}
}

// significant applies the noise gate: |Δ| ≥ max(rel × |base|, abs, k × sd).
func significant(cur, base, sd, rel, abs float64) bool {
	if math.IsNaN(cur) || math.IsNaN(base) {
		return false
	}
	return math.Abs(cur-base) >= math.Max(math.Max(rel*math.Abs(base), abs), kStdev*sd)
}

func pctDelta(cur, base float64) float64 {
	if base == 0 {
		return math.NaN()
	}
	return (cur - base) / math.Abs(base) * 100
}

// weeklyBaseline returns the mean and stdev of the 4 weekly means before the period.
func (c *Ctx) weeklyBaseline(m Metric, p Period) (mean_, sd float64, ok bool) {
	w := c.Data.weeklyMeans(m, p.Cur.From, 4, minWatchDaysWeek)
	if len(w) < minBaselineWeeks {
		return 0, 0, false
	}
	return mathx.Mean(w), mathx.Stdev(w), true
}

// weekSD is the stdev of weekly means inside a range (used for 28-day blocks).
func (c *Ctx) weekSD(m Metric, r days.Range) float64 {
	var w []float64
	for s := r.From; s+6 <= r.To; s += 7 {
		v := c.Data.values(m, days.Range{From: s, To: s + 6}, nil)
		if len(v) >= minWatchDaysWeek {
			w = append(w, mathx.Mean(v))
		}
	}
	return mathx.Stdev(w)
}

func enoughWeek(c *Ctx, r days.Range) bool { return c.Data.watchDays(r) >= minWatchDaysWeek }

// enoughBlock requires ≥ 5 Watch days per week on average.
func enoughBlock(c *Ctx, r days.Range) bool {
	return c.Data.watchDays(r) >= r.Len()/7*minWatchDaysWeek
}

func stepsTrend(c *Ctx, p Period) *Insight {
	if !enoughWeek(c, p.Cur) {
		return nil
	}
	cur, n := c.Data.mean(mSteps, p.Cur)
	base, sd, ok := c.weeklyBaseline(mSteps, p)
	if !ok || !significant(cur, base, sd, 0.10, 300) {
		return nil
	}
	d := pctDelta(cur, base)
	return &Insight{
		Polarity: pick(cur > base, Positive, Attention),
		Title:    fmt.Sprintf("You walked %s %s {period}", pctStr(d), moreLess(d)),
		Body:     fmt.Sprintf("%s vs %s steps/day, your 4-week average.", num(cur, 0), num(base, 0)),
		Metric:   "steps", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(d),
		Evidence: map[string]any{"days_with_data": n, "baseline_weeks": 4, "baseline_weekly_sd": round1(sd), "unit": "steps/day"},
	}
}

func kcalTrend(c *Ctx, p Period) *Insight {
	if !enoughWeek(c, p.Cur) {
		return nil
	}
	cur, n := c.Data.mean(mKcal, p.Cur)
	base, sd, ok := c.weeklyBaseline(mKcal, p)
	if !ok || !significant(cur, base, sd, 0.10, 30) {
		return nil
	}
	d := pctDelta(cur, base)
	return &Insight{
		Polarity: pick(cur > base, Positive, Attention),
		Title:    fmt.Sprintf("Active calories are %s %s your usual level", pctStr(d), aboveBelow(d)),
		Body:     fmt.Sprintf("%s vs %s kcal/day, your 4-week average.", num(cur, 0), num(base, 0)),
		Metric:   "active_kcal", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(d),
		Evidence: map[string]any{"days_with_data": n, "baseline_weekly_sd": round1(sd), "unit": "kcal/day"},
	}
}

// monthlyTrend compares the 28-day block with the previous one, with the 90-day median as context.
func monthlyTrend(metric string, m Metric, rel, abs float64, unit string, scale float64, upGood bool, label string) func(*Ctx, Period) *Insight {
	return func(c *Ctx, p Period) *Insight {
		prev := p.Prev(1)
		if !enoughBlock(c, p.Cur) || !enoughBlock(c, prev) {
			return nil
		}
		cur, _ := c.Data.mean(m, p.Cur)
		base, _ := c.Data.mean(m, prev)
		sd := c.weekSD(m, prev)
		if !significant(cur, base, sd, rel, abs) {
			return nil
		}
		d := pctDelta(cur, base)
		med90 := mathx.Median(c.Data.values(m, days.Range{From: p.Cur.To.Add(-89), To: p.Cur.To}, nil))
		show := func(v float64) string {
			if scale > 0 {
				return num(v/1000, 1)
			}
			return num(v, 0)
		}
		return &Insight{
			Polarity: pick((cur > base) == upGood, Positive, Attention),
			Title:    fmt.Sprintf("%s %s %s {period}", label, upDown(d), pctStr(d)),
			Body: fmt.Sprintf("%s vs %s %s in the 28 days before; 90-day median %s.",
				show(cur), show(base), unit, show(med90)),
			Metric: metric, Current: fp(cur), Baseline: fp(base), DeltaPct: fp(d),
			Evidence: map[string]any{"previous_period": rangeMap(prev), "median_90d": fp(med90), "baseline_weekly_sd": round1(sd)},
		}
	}
}

func goalAttainment(c *Ctx, p Period) *Insight {
	if !enoughWeek(c, p.Cur) {
		return nil
	}
	goal := c.Res.Goals.StepsPerDay
	hit := func(r days.Range) (int, int) {
		v := c.Data.values(mSteps, r, nil)
		n := 0
		for _, x := range v {
			if x >= goal {
				n++
			}
		}
		return n, len(v)
	}
	cur, total := hit(p.Cur)
	var base []float64
	for i := 1; i <= 4; i++ {
		r := p.Prev(i)
		if n, t := hit(r); t >= minWatchDaysWeek {
			base = append(base, float64(n))
		}
	}
	if len(base) < minBaselineWeeks {
		return nil
	}
	usual := mathx.Mean(base)
	if math.Abs(float64(cur)-usual) < 2 {
		return nil
	}
	return &Insight{
		Polarity: pick(float64(cur) > usual, Positive, Attention),
		Title:    fmt.Sprintf("Step goal reached on %d of %d days", cur, total),
		Body:     fmt.Sprintf("Goal %s steps; usually reached on %s days a week.", num(goal, 0), num(usual, 0)),
		Metric:   "step_goal_days", Current: fp(float64(cur)), Baseline: fp(usual), DeltaPct: fp(pctDelta(float64(cur), usual)),
		Evidence: map[string]any{"goal_steps": goal, "days_with_data": total},
	}
}

func isWorkday(d days.Day, _ *Day) bool { return !d.IsWeekend() }

func sedentary(c *Ctx, p Period) *Insight {
	prev := p.Prev(1)
	if !enoughBlock(c, p.Cur) || !enoughBlock(c, prev) {
		return nil
	}
	curV := c.Data.values(mSedentary, p.Cur, isWorkday)
	baseV := c.Data.values(mSedentary, prev, isWorkday)
	if len(curV) < 10 || len(baseV) < 10 {
		return nil
	}
	cur, base := mathx.Mean(curV), mathx.Mean(baseV)
	if !significant(cur, base, 0, 0.15, 0.5) {
		return nil
	}
	return &Insight{
		Polarity: pick(cur > base, Attention, Positive),
		Title:    fmt.Sprintf("Longest sitting stretch %s on workdays", pick(cur > base, "grew", "shrank")),
		Body:     fmt.Sprintf("%s h → %s h on average, compared with the 28 days before.", num(base, 1), num(cur, 1)),
		Metric:   "longest_sedentary_h", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		Evidence: map[string]any{"workdays": len(curV), "previous_workdays": len(baseV), "previous_period": rangeMap(prev)},
	}
}

func weekdayWeekend(c *Ctx, p Period) *Insight {
	if !enoughBlock(c, p.Cur) {
		return nil
	}
	work := c.Data.values(mSteps, p.Cur, isWorkday)
	wkend := c.Data.values(mSteps, p.Cur, func(d days.Day, _ *Day) bool { return d.IsWeekend() })
	if len(work) < 10 || len(wkend) < 4 {
		return nil
	}
	w, e := mathx.Mean(work), mathx.Mean(wkend)
	if w <= 0 || e <= 0 {
		return nil
	}
	ratio := e / w
	if ratio < 1.3 && ratio > 1/1.3 {
		return nil
	}
	var body string
	if ratio >= 1 {
		body = fmt.Sprintf("%s× the steps of workdays: %s vs %s steps/day.", num(ratio, 1), num(e, 0), num(w, 0))
	} else {
		body = fmt.Sprintf("%s× the steps of weekend days: %s vs %s steps/day.", num(1/ratio, 1), num(w, 0), num(e, 0))
	}
	return &Insight{
		Polarity: Neutral,
		Title:    pick(ratio >= 1, "You move more on weekends", "You move more on workdays"),
		Body:     body, Metric: "steps_weekend_ratio", Current: fp(e), Baseline: fp(w), DeltaPct: fp(pctDelta(e, w)),
		Evidence: map[string]any{"workdays": len(work), "weekend_days": len(wkend), "ratio": fp(ratio)},
	}
}

func gymVsRest(c *Ctx, p Period) *Insight {
	if !enoughBlock(c, p.Cur) {
		return nil
	}
	gym := c.Data.values(mSteps, p.Cur, func(_ days.Day, d *Day) bool { return d.Gym })
	rest := c.Data.values(mSteps, p.Cur, func(_ days.Day, d *Day) bool { return !d.Gym })
	if len(gym) < 4 || len(rest) < 4 {
		return nil
	}
	g, r := mathx.Mean(gym), mathx.Mean(rest)
	goal := c.Res.Goals.StepsPerDay
	if r >= goal || r >= 0.85*g {
		return nil
	}
	return &Insight{
		Polarity: Neutral,
		Title:    fmt.Sprintf("Non-training days average %s steps", num(r, 0)),
		Body:     fmt.Sprintf("Training days average %s — consider short walks on the other days.", num(g, 0)),
		Metric:   "steps_non_gym_days", Current: fp(r), Baseline: fp(g), DeltaPct: fp(pctDelta(r, g)),
		Evidence: map[string]any{"gym_days": len(gym), "non_gym_days": len(rest), "goal_steps": goal},
	}
}

func rings(c *Ctx, p Period) *Insight {
	var closed, ex, stand, n int
	for d := p.Cur.From; d <= p.Cur.To; d++ {
		day, ok := c.Data.Days[d]
		if !ok || math.IsNaN(day.MoveKcal) || math.IsNaN(day.MoveGoal) || day.MoveGoal <= 0 {
			continue
		}
		n++
		if day.MoveKcal >= day.MoveGoal {
			closed++
		}
		if !math.IsNaN(day.ExerciseMin) && !math.IsNaN(day.ExGoal) && day.ExGoal > 0 && day.ExerciseMin >= day.ExGoal {
			ex++
		}
		if !math.IsNaN(day.StandHours) && !math.IsNaN(day.StandGoal) && day.StandGoal > 0 && day.StandHours >= day.StandGoal {
			stand++
		}
	}
	if n < minWatchDaysWeek || (closed > 2 && closed < 5) {
		return nil
	}
	return &Insight{
		Polarity: pick(closed >= 5, Positive, Attention),
		Title:    fmt.Sprintf("Move ring closed %d/%d days", closed, n),
		Body:     fmt.Sprintf("Exercise ring %d/%d, stand ring %d/%d.", ex, n, stand, n),
		Metric:   "move_ring_days", Current: fp(float64(closed)),
		Evidence: map[string]any{"days": n, "exercise_closed": ex, "stand_closed": stand},
	}
}

// bpmTrend compares the 28-day block with the two blocks before it (≈ 2 months); lower is better.
func bpmTrend(metric string, m Metric, label string, abs float64) func(*Ctx, Period) *Insight {
	return func(c *Ctx, p Period) *Insight {
		cv := c.Data.values(m, p.Cur, nil)
		base := days.Range{From: p.Prev(2).From, To: p.Prev(1).To}
		bv := c.Data.values(m, base, nil)
		if len(cv) < 10 || len(bv) < 20 {
			return nil
		}
		cur, b := mathx.Mean(cv), mathx.Mean(bv)
		sd := c.weekSD(m, base)
		if !significant(cur, b, sd, 0, abs) {
			return nil
		}
		delta := cur - b
		word := pick(delta < 0, "improved", "rose")
		if metric == "walking_hr" {
			word = pick(delta < 0, "decreased", "increased")
		}
		body := fmt.Sprintf("%s → %s bpm, compared with the 2 months before.", num(b, 0), num(cur, 0))
		if metric == "walking_hr" && delta < 0 {
			body = fmt.Sprintf("%s → %s bpm — a sign of improved fitness.", num(b, 0), num(cur, 0))
		}
		return &Insight{
			Polarity: pick(delta < 0, Positive, Attention),
			Title:    fmt.Sprintf("%s %s by %s bpm", label, word, num(math.Abs(delta), 0)),
			Body:     body, Metric: metric, Current: fp(cur), Baseline: fp(b), DeltaPct: fp(pctDelta(cur, b)),
			Evidence: map[string]any{"days_with_data": len(cv), "baseline_days": len(bv), "baseline_period": rangeMap(base),
				"baseline_weekly_sd": round1(sd), "unit": "bpm"},
		}
	}
}

func restingSpike(c *Ctx, p Period) *Insight {
	end := p.Cur.To
	baseV := c.Data.values(mRHR, days.Range{From: end.Add(-33), To: end.Add(-4)}, nil)
	if len(baseV) < 14 {
		return nil
	}
	base := mathx.Median(baseV)
	val := func(d days.Day) float64 {
		if day, ok := c.Data.Days[d]; ok {
			return day.RestingHR
		}
		return math.NaN()
	}
	if !(val(end) >= base+5) {
		return nil
	}
	start := end
	for val(start.Add(-1)) >= base+5 {
		start--
	}
	n := int(end-start) + 1
	if n < 3 {
		return nil
	}
	cur := mathx.Mean(c.Data.values(mRHR, days.Range{From: start, To: end}, nil))
	return &Insight{
		Polarity: Attention,
		Title:    fmt.Sprintf("Resting HR elevated for %d days", n),
		Body:     fmt.Sprintf("%s vs %s bpm — 5+ bpm above your baseline each day.", num(cur, 0), num(base, 0)),
		Metric:   "resting_hr", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		PeriodStart: start.String(), PeriodEnd: end.String(),
		Evidence: map[string]any{"days": n, "baseline_days": len(baseV), "threshold_bpm": 5},
	}
}

func hrvTrend(c *Ctx, p Period) *Insight {
	cv := c.Data.values(mHRV, p.Cur, nil)
	bv := c.Data.values(mHRV, days.Range{From: p.Cur.From.Add(-90), To: p.Cur.From.Add(-1)}, nil)
	if len(cv) < 4 || len(bv) < 30 {
		return nil
	}
	cur, base := mathx.Mean(cv), mathx.Median(bv)
	_, sd, _ := c.weeklyBaseline(mHRV, p)
	if !significant(cur, base, sd, 0.10, 2) {
		return nil
	}
	d := pctDelta(cur, base)
	suffix := "."
	if d > 0 {
		suffix = " — good recovery."
	}
	return &Insight{
		Polarity: pick(d > 0, Positive, Attention),
		Title:    fmt.Sprintf("HRV is %s %s your 90-day median", pctStr(d), aboveBelow(d)),
		Body:     fmt.Sprintf("%s vs %s ms%s", num(cur, 0), num(base, 0), suffix),
		Metric:   "hrv_sdnn", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(d),
		Evidence: map[string]any{"days_with_data": len(cv), "baseline_days": len(bv), "unit": "ms"},
	}
}

func vo2Change(c *Ctx, p Period) *Insight {
	cv := c.Data.values(mVO2, p.Cur, nil)
	prev := p.Prev(1)
	bv := c.Data.values(mVO2, prev, nil)
	if len(cv) < 3 || len(bv) < 3 {
		return nil
	}
	cur, base := mathx.Mean(cv), mathx.Mean(bv)
	if math.Abs(cur-base) < 0.5 {
		return nil
	}
	return &Insight{
		Polarity: pick(cur > base, Positive, Attention),
		Title:    fmt.Sprintf("VO2max estimate %s", pick(cur > base, "rose", "fell")),
		Body:     fmt.Sprintf("%s → %s mL/(kg·min), compared with the 13 weeks before.", num(base, 1), num(cur, 1)),
		Metric:   "vo2max", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		Evidence: map[string]any{"samples": len(cv), "previous_samples": len(bv), "previous_period": rangeMap(prev), "unit": "mL/(kg·min)"},
	}
}

func heartEvents(c *Ctx, p Period) *Insight {
	n := 0
	for d := p.Cur.From; d <= p.Cur.To; d++ {
		if day, ok := c.Data.Days[d]; ok {
			n += day.HeartEvents
		}
	}
	if n == 0 {
		return nil
	}
	return &Insight{
		Polarity: Neutral,
		Title:    fmt.Sprintf("%d heart notification%s {period}", n, plural(n)),
		Body:     "High, low or irregular rhythm alerts. See the Heart page for details.",
		Metric:   "heart_events", Current: fp(float64(n)),
	}
}

func consistency(c *Ctx, p Period) *Insight {
	target := c.Res.Goals.SessionsPerWeek
	sessions := len(c.Data.workoutsIn(p.Cur))
	// streak of weeks meeting the target, ending with this week
	var weeks []derive.Week
	for w := c.Data.First.ISOWeekStart(); w <= p.Cur.From; w += 7 {
		weeks = append(weeks, derive.Week{Met: len(c.Data.workoutsIn(days.Range{From: w, To: w + 6})) >= target})
	}
	streak, _ := derive.Streaks(weeks)
	if sessions >= target {
		return &Insight{
			Polarity: Positive,
			Title:    fmt.Sprintf("%d/%d sessions {period}", sessions, target),
			Body:     pick(streak > 1, fmt.Sprintf("Target met %d weeks in a row.", streak), "Weekly target met."),
			Metric:   "sessions", Current: fp(float64(sessions)), Baseline: fp(float64(target)),
			Evidence: map[string]any{"target": target, "streak_weeks": streak},
		}
	}
	if c.Data.watchDays(p.Cur) < 3 {
		return nil // probably not wearing the Watch
	}
	return &Insight{
		Polarity: Attention,
		Title:    fmt.Sprintf("Only %d session%s {period}", sessions, plural(sessions)),
		Body:     fmt.Sprintf("Your target is %d a week.", target),
		Metric:   "sessions", Current: fp(float64(sessions)), Baseline: fp(float64(target)),
		Evidence: map[string]any{"target": target},
	}
}

func volume(ws []Workout) (int, float64) {
	var h float64
	for _, w := range ws {
		h += w.DurationS / 3600
	}
	return len(ws), h
}

func monthlyVolume(c *Ctx, p Period) *Insight {
	prev := p.Prev(1)
	cn, ch := volume(c.Data.workoutsIn(p.Cur))
	pn, ph := volume(c.Data.workoutsIn(prev))
	if cn+pn == 0 || prev.From < c.Data.First {
		return nil
	}
	if abs(cn-pn) < 2 && math.Abs(ch-ph) < 0.15*math.Max(ph, 1) {
		return nil
	}
	pol := Neutral
	if ph > 0 && ch < 0.75*ph {
		pol = Attention
	}
	return &Insight{
		Polarity: pol,
		Title:    fmt.Sprintf("%d sessions / %s h {period}", cn, num(ch, 1)),
		Body:     fmt.Sprintf("%d sessions / %s h in the 28 days before.", pn, num(ph, 1)),
		Metric:   "training_hours", Current: fp(ch), Baseline: fp(ph), DeltaPct: fp(pctDelta(ch, ph)),
		Evidence: map[string]any{"sessions": cn, "previous_sessions": pn, "previous_period": rangeMap(prev)},
	}
}

// avgOf averages get over the workouts for which it reports a value.
func avgOf(ws []Workout, get func(Workout) (float64, bool)) (float64, int) {
	var v []float64
	for _, w := range ws {
		if x, ok := get(w); ok {
			v = append(v, x)
		}
	}
	return mathx.Mean(v), len(v)
}

func intensity(c *Ctx, p Period) *Insight {
	prev := p.Prev(1)
	get := func(w Workout) (float64, bool) { return w.PctZ3, w.PctZ3 >= 0 } // -1 = no HR data
	cur, cn := avgOf(c.Data.workoutsIn(p.Cur), get)
	base, bn := avgOf(c.Data.workoutsIn(prev), get)
	if cn < 4 || bn < 4 || math.Abs(cur-base) < 5 {
		return nil
	}
	return &Insight{
		Polarity: Neutral,
		Title:    fmt.Sprintf("Average session intensity %s", pick(cur > base, "rose", "fell")),
		Body:     fmt.Sprintf("%s %% of session time in zone 3+, was %s %%.", num(cur, 0), num(base, 0)),
		Metric:   "pct_z3_plus", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		Evidence: map[string]any{"sessions": cn, "previous_sessions": bn},
	}
}

func sessionHR(c *Ctx, p Period) *Insight {
	cw := c.Data.workoutsIn(p.Cur)
	bw := c.Data.workoutsIn(p.Prev(2))
	if len(cw) < 4 || len(bw) < 4 {
		return nil
	}
	var durs []float64
	for _, w := range append(append([]Workout{}, cw...), bw...) {
		durs = append(durs, w.DurationS)
	}
	med := mathx.Median(durs)
	similar := func(w Workout) (float64, bool) {
		return w.AvgHR, w.AvgHR > 0 && math.Abs(w.DurationS-med) <= 0.2*med
	}
	cur, cn := avgOf(cw, similar)
	base, bn := avgOf(bw, similar)
	if cn < 3 || bn < 3 || math.Abs(cur-base) < 3 {
		return nil
	}
	return &Insight{
		Polarity: pick(cur < base, Positive, Neutral),
		Title:    fmt.Sprintf("Session HR %s %s bpm", upDown(cur-base), num(math.Abs(cur-base), 0)),
		Body: fmt.Sprintf("%s → %s bpm over 8 weeks, comparing sessions of similar length (~%s min).",
			num(base, 0), num(cur, 0), num(med/60, 0)),
		Metric: "session_avg_hr", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		Evidence: map[string]any{"sessions": cn, "baseline_sessions": bn, "baseline_period": rangeMap(p.Prev(2)), "median_duration_min": round1(med / 60)},
	}
}

func loadForm(c *Ctx, p Period) *Insight {
	day, ok := c.Data.Days[p.Cur.To]
	if !ok || len(c.Data.workoutsIn(days.Range{From: p.Cur.To.Add(-41), To: p.Cur.To})) < 4 {
		return nil
	}
	switch {
	case !math.IsNaN(day.ACWR) && day.ACWR >= 1.5:
		return &Insight{
			Polarity: Attention, Title: fmt.Sprintf("Training load jumped (ACWR %s)", num(day.ACWR, 1)),
			Body:   "Recent load is well above your usual level — watch recovery.",
			Metric: "acwr", Current: fp(day.ACWR),
			Evidence: map[string]any{"ctl": round1(day.CTL), "atl": round1(day.ATL), "tsb": round1(day.TSB)},
		}
	case !math.IsNaN(day.ACWR) && day.ACWR > 0 && day.ACWR <= 0.6:
		return &Insight{
			Polarity: Neutral, Title: fmt.Sprintf("Training load dropped (ACWR %s)", num(day.ACWR, 1)),
			Body:   "Recent load is well below your usual level.",
			Metric: "acwr", Current: fp(day.ACWR),
			Evidence: map[string]any{"ctl": round1(day.CTL), "atl": round1(day.ATL), "tsb": round1(day.TSB)},
		}
	case !math.IsNaN(day.TSB) && day.TSB > 5 && day.CTL > 10:
		return &Insight{
			Polarity: Positive, Title: "Form is positive",
			Body:   fmt.Sprintf("TSB %s — well recovered.", num(day.TSB, 0)),
			Metric: "tsb", Current: fp(day.TSB),
			Evidence: map[string]any{"ctl": round1(day.CTL), "atl": round1(day.ATL), "acwr": fp(day.ACWR)},
		}
	}
	return nil
}

func durationDrift(c *Ctx, p Period) *Insight {
	get := func(w Workout) (float64, bool) { return w.DurationS, w.DurationS > 0 }
	cur, cn := avgOf(c.Data.workoutsIn(p.Cur), get)
	base := days.Range{From: p.Prev(2).From, To: p.Prev(1).To}
	b, bn := avgOf(c.Data.workoutsIn(base), get)
	if cn < 4 || bn < 6 || math.Abs(cur-b) < math.Max(0.10*b, 300) {
		return nil
	}
	return &Insight{
		Polarity: pick(cur < b, Attention, Neutral),
		Title:    fmt.Sprintf("Sessions are getting %s", pick(cur < b, "shorter", "longer")),
		Body:     fmt.Sprintf("%s min on average vs %s min in the 8 weeks before.", num(cur/60, 0), num(b/60, 0)),
		Metric:   "session_duration_min", Current: fp(cur / 60), Baseline: fp(b / 60), DeltaPct: fp(pctDelta(cur, b)),
		Evidence: map[string]any{"sessions": cn, "baseline_sessions": bn, "baseline_period": rangeMap(base)},
	}
}

// NextDayValues collects the resting HR and HRV of days in r that follow a day with Watch data,
// split by whether that day had a main-type workout. The recovery rule and the Sleep page
// both use it.
type NextDayValues struct {
	AfterGymRHR, AfterRestRHR, AfterGymHRV, AfterRestHRV []float64
}

func (data *Data) NextDay(r days.Range) NextDayValues {
	var v NextDayValues
	for d := r.From; d <= r.To; d++ {
		prev, ok1 := data.Days[d.Add(-1)]
		day, ok2 := data.Days[d]
		if !ok1 || !ok2 || !prev.Watch {
			continue
		}
		if prev.Gym {
			v.AfterGymRHR = appendNum(v.AfterGymRHR, day.RestingHR)
			v.AfterGymHRV = appendNum(v.AfterGymHRV, day.HRV)
		} else {
			v.AfterRestRHR = appendNum(v.AfterRestRHR, day.RestingHR)
			v.AfterRestHRV = appendNum(v.AfterRestHRV, day.HRV)
		}
	}
	return v
}

func nextDay(c *Ctx, p Period) *Insight {
	v := c.Data.NextDay(p.Cur)
	afterGymRHR, afterRestRHR, afterGymHRV, afterRestHRV := v.AfterGymRHR, v.AfterRestRHR, v.AfterGymHRV, v.AfterRestHRV
	if len(afterGymRHR) < 8 || len(afterRestRHR) < 8 {
		return nil
	}
	dr := mathx.Mean(afterGymRHR) - mathx.Mean(afterRestRHR)
	dh := pctDelta(mathx.Mean(afterGymHRV), mathx.Mean(afterRestHRV))
	if math.Abs(dr) < 1.5 && (math.IsNaN(dh) || math.Abs(dh) < 5) {
		return nil
	}
	parts := []string{fmt.Sprintf("resting HR is on average %s bpm %s", num(math.Abs(dr), 0), pick(dr >= 0, "higher", "lower"))}
	if !math.IsNaN(dh) && len(afterGymHRV) >= 8 && len(afterRestHRV) >= 8 {
		parts = append(parts, fmt.Sprintf("HRV %s %s", pctStr(dh), pick(dh >= 0, "higher", "lower")))
	}
	return &Insight{
		Polarity: Neutral,
		Title:    "How training affects the next day",
		Body:     fmt.Sprintf("On days after training, %s than after rest days.", strings.Join(parts, " and ")),
		Metric:   "next_day_resting_hr_delta", Current: fp(mathx.Mean(afterGymRHR)), Baseline: fp(mathx.Mean(afterRestRHR)), DeltaPct: fp(dh),
		Evidence: map[string]any{"days_after_training": len(afterGymRHR), "days_after_rest": len(afterRestRHR),
			"hrv_after_training_ms": fp(mathx.Mean(afterGymHRV)), "hrv_after_rest_ms": fp(mathx.Mean(afterRestHRV))},
	}
}

func sleepTrend(c *Ctx, p Period) *Insight {
	cv := c.Data.values(mSleep, p.Cur, nil)
	if len(cv) < 4 {
		return nil
	}
	w := c.Data.weeklyMeans(mSleep, p.Cur.From, 4, 4)
	if len(w) < minBaselineWeeks {
		return nil
	}
	cur, base, sd := mathx.Mean(cv), mathx.Mean(w), mathx.Stdev(w)
	if !significant(cur, base, sd, 0, 20) {
		return nil
	}
	return &Insight{
		Polarity: pick(cur < base, Attention, Positive),
		Title:    fmt.Sprintf("Average sleep %s {period}", hm(cur)),
		Body:     fmt.Sprintf("Usually %s.", hm(base)),
		Metric:   "asleep_min", Current: fp(cur), Baseline: fp(base), DeltaPct: fp(pctDelta(cur, base)),
		Evidence: map[string]any{"nights": len(cv), "baseline_weekly_sd_min": round1(sd)},
	}
}

func sleepHRV(c *Ctx, p Period) *Insight {
	var short, normal []float64
	for d := p.Cur.From; d <= p.Cur.To; d++ {
		day, ok := c.Data.Days[d]
		if !ok || math.IsNaN(day.AsleepMin) || math.IsNaN(day.HRV) {
			continue
		}
		if day.AsleepMin < 360 {
			short = append(short, day.HRV)
		} else {
			normal = append(normal, day.HRV)
		}
	}
	if len(short) < 5 || len(normal) < 10 {
		return nil
	}
	d := pctDelta(mathx.Mean(short), mathx.Mean(normal))
	if math.Abs(d) < 8 {
		return nil
	}
	return &Insight{
		Polarity: pick(d < 0, Attention, Neutral),
		Title:    fmt.Sprintf("Short nights are followed by %s %s HRV", pctStr(d), pick(d < 0, "lower", "higher")),
		Body:     fmt.Sprintf("%s vs %s ms after nights under 6 h and other nights.", num(mathx.Mean(short), 0), num(mathx.Mean(normal), 0)),
		Metric:   "hrv_after_short_sleep", Current: fp(mathx.Mean(short)), Baseline: fp(mathx.Mean(normal)), DeltaPct: fp(d),
		Evidence: map[string]any{"short_nights": len(short), "other_nights": len(normal)},
	}
}

// --- formatting helpers ---

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func moreLess(d float64) string   { return pick(d >= 0, "more", "less") }
func aboveBelow(d float64) string { return pick(d >= 0, "above", "below") }
func upDown(d float64) string     { return pick(d >= 0, "up", "down") }

func pctStr(d float64) string { return strconv.Itoa(int(math.Round(math.Abs(d)))) + " %" }

// num formats with a narrow no-break space (U+202F) as thousands separator, e.g. 5 900.
func num(v float64, prec int) string {
	if math.IsNaN(v) {
		return "–"
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	if len(intPart) > 3 {
		var b strings.Builder
		for i, ch := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteString(" ")
			}
			b.WriteRune(ch)
		}
		intPart = b.String()
	}
	if neg {
		intPart = "-" + intPart
	}
	return intPart + frac
}

func hm(min float64) string {
	m := int(math.Round(min))
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

func plural(n int) string { return pick(n == 1, "", "s") }

func round1(v float64) *float64 { return mathx.RoundPtr(v, 1) }

func rangeMap(r days.Range) map[string]string {
	return map[string]string{"from": r.From.String(), "to": r.To.String()}
}

func appendNum(s []float64, v float64) []float64 {
	if math.IsNaN(v) {
		return s
	}
	return append(s, v)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
