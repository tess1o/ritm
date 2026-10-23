package insights

import (
	"math"
	"strings"
	"testing"

	"ritm/internal/days"
	"ritm/internal/derive"
)

var anchor = days.MustParse("2026-10-18") // a Sunday

func res() *derive.Resolved {
	return &derive.Resolved{Settings: derive.DefaultSettings(), MainType: "FST"}
}

// mkData builds n days ending at anchor with defaults overridden by fill.
func mkData(n int, fill func(d days.Day, i int, day *Day)) *Data {
	data := &Data{Days: map[days.Day]*Day{}, First: anchor.Add(-n + 1), Anchor: anchor, Today: anchor.Add(3)}
	for i := 0; i < n; i++ {
		d := data.First.Add(i)
		day := &Day{Steps: 8000 + float64(i%3)*100, DistanceM: 6000, ActiveKcal: 500, StandHours: 11, ExerciseMin: 30, Sedentary: 2,
			MoveKcal: math.NaN(), MoveGoal: math.NaN(), ExGoal: math.NaN(), StandGoal: math.NaN(), Watch: true,
			RestingHR: 58, WalkingHR: 95, HRV: 50, VO2Max: math.NaN(), AsleepMin: 420,
			Load: math.NaN(), CTL: math.NaN(), ATL: math.NaN(), TSB: math.NaN(), ACWR: math.NaN()}
		if fill != nil {
			fill(d, i, day)
		}
		data.Days[d] = day
	}
	return data
}

func lastWeek() Period { return periodsEndingBy(Weekly, anchor, 1)[0] }

func TestPeriodsAlignToBlocks(t *testing.T) {
	w := lastWeek()
	if w.Cur.From.String() != "2026-10-12" || w.Cur.To != anchor {
		t.Fatalf("week %s", w.Cur)
	}
	m := periodsEndingBy(Monthly, anchor, 2)
	if m[0].Cur.Len() != 28 || m[1].Cur.To != m[0].Cur.From-1 || m[0].Cur.To > anchor || m[0].Cur.From.Weekday() != 1 {
		t.Fatalf("months %v", m)
	}
	if again := periodsEndingBy(Monthly, anchor.Add(1), 1)[0]; again != m[0] {
		t.Fatal("monthly block must be stable across days")
	}
}

func TestStepsTrend(t *testing.T) {
	w := lastWeek()
	fire := mkData(60, func(d days.Day, _ int, day *Day) {
		if w.Cur.Contains(d) {
			day.Steps = 5900
		}
	})
	in := stepsTrend(&Ctx{fire, res()}, w)
	if in == nil || in.Polarity != Attention || !strings.Contains(in.Title, "less {period}") {
		t.Fatalf("expected attention insight, got %+v", in)
	}
	if in2 := stepsTrend(&Ctx{mkData(60, nil), res()}, w); in2 != nil {
		t.Fatalf("stable steps should not fire: %s", in2.Body)
	}
	// noisy baseline: weekly means swing by ±3000, a 20 % drop is within noise
	noisy := mkData(60, func(d days.Day, _ int, day *Day) {
		if w.Cur.Contains(d) {
			day.Steps = 6500
		} else if ((d-epochMonday)/7)%2 == 0 {
			day.Steps = 11000
		} else {
			day.Steps = 5000
		}
	})
	if in3 := stepsTrend(&Ctx{noisy, res()}, w); in3 != nil {
		t.Fatalf("noisy baseline should not fire: %s", in3.Body)
	}
	sparse := mkData(60, func(d days.Day, _ int, day *Day) {
		if w.Cur.Contains(d) {
			day.Steps = 3000
			day.Watch = d.Weekday() < 3
		}
	})
	if in4 := stepsTrend(&Ctx{sparse, res()}, w); in4 != nil {
		t.Fatal("insufficient Watch days should not fire")
	}
}

func TestRestingSpike(t *testing.T) {
	data := mkData(60, func(d days.Day, _ int, day *Day) {
		if d > anchor.Add(-3) {
			day.RestingHR = 65
		}
	})
	in := restingSpike(&Ctx{data, res()}, Period{Kind: Daily, Cur: days.Range{From: anchor, To: anchor}})
	if in == nil || in.PeriodStart != anchor.Add(-2).String() || !strings.Contains(in.Title, "3 days") {
		t.Fatalf("spike: %+v", in)
	}
	two := mkData(60, func(d days.Day, _ int, day *Day) {
		if d > anchor.Add(-2) {
			day.RestingHR = 65
		}
	})
	if restingSpike(&Ctx{two, res()}, Period{Kind: Daily, Cur: days.Range{From: anchor, To: anchor}}) != nil {
		t.Fatal("2-day spike should not fire")
	}
}

func TestConsistency(t *testing.T) {
	data := mkData(60, nil)
	w := lastWeek()
	for wk := w.Cur.From; wk >= w.Cur.From.Add(-14); wk -= 7 {
		for i := 0; i < 3; i++ {
			data.Workouts = append(data.Workouts, Workout{Day: wk.Add(i * 2), DurationS: 3600, AvgHR: 120})
		}
	}
	in := consistency(&Ctx{data, res()}, w)
	if in == nil || in.Polarity != Positive || !strings.Contains(in.Body, "3 weeks in a row") {
		t.Fatalf("consistency: %+v", in)
	}
	data.Workouts = data.Workouts[3:] // drop this week's sessions
	in = consistency(&Ctx{data, res()}, w)
	if in == nil || in.Polarity != Attention || !strings.Contains(in.Title, "Only 0 sessions") {
		t.Fatalf("missed sessions: %+v", in)
	}
}

func TestSleepAndHRV(t *testing.T) {
	w := lastWeek()
	data := mkData(120, func(d days.Day, _ int, day *Day) {
		if w.Cur.Contains(d) {
			day.AsleepMin = 370
			day.HRV = 60
		}
	})
	if in := sleepTrend(&Ctx{data, res()}, w); in == nil || !strings.Contains(in.Title, "6 h 10 min {period}") || !strings.Contains(in.Body, "Usually 7 h 0 min") {
		t.Fatalf("sleep: %+v", in)
	}
	if in := hrvTrend(&Ctx{data, res()}, w); in == nil || in.Polarity != Positive {
		t.Fatalf("hrv: %+v", in)
	}
	if in := sleepTrend(&Ctx{mkData(120, nil), res()}, w); in != nil {
		t.Fatal("stable sleep should not fire")
	}
}

func TestEvaluateAllRulesNoPanic(t *testing.T) {
	data := mkData(400, func(d days.Day, i int, day *Day) {
		day.Steps = 6000 + float64(i*37%4000)
		day.RestingHR = 62 - float64(i)/100
		day.Gym = d.Weekday() == 1 || d.Weekday() == 3 || d.Weekday() == 5
		day.VO2Max = 38 + float64(i)/200
		day.MoveKcal, day.MoveGoal = 520, 500
		day.ACWR, day.TSB, day.CTL = 1.0, 6, 30
		if i%11 == 0 {
			day.HeartEvents = 1
		}
		if day.Gym {
			day.Steps = 9000
		}
	})
	for d := data.First; d <= anchor; d++ {
		if data.Days[d].Gym {
			data.Workouts = append(data.Workouts, Workout{Day: d, DurationS: 3600 - float64(int(d)%5)*60, AvgHR: 125, PctZ3: 30})
		}
	}
	out := Evaluate(data, res())
	if len(out) == 0 {
		t.Fatal("no insights")
	}
	seen := map[string]bool{}
	for _, in := range out {
		key := in.RuleID + in.PeriodKind + in.PeriodStart
		if seen[key] {
			t.Fatalf("duplicate insight %s", key)
		}
		seen[key] = true
		if in.Title == "" || in.Body == "" || (in.Polarity != Positive && in.Polarity != Neutral && in.Polarity != Attention) {
			t.Fatalf("bad insight %+v", in)
		}
	}
	s := ComputeScores(data, res(), anchor)
	if s.Activity.Value == nil || s.Heart.Value == nil || s.Training.Value == nil {
		t.Fatalf("scores %+v", s)
	}
}

func TestNum(t *testing.T) {
	if num(5900, 0) != "5\u202f900" || num(12345.6, 1) != "12\u202f345.6" || num(950, 0) != "950" {
		t.Fatalf("%q %q", num(5900, 0), num(12345.6, 1))
	}
}

func TestPeriodPhrase(t *testing.T) {
	today := days.MustParse("2026-10-22") // Thursday
	cases := []struct{ kind, start, end, want string }{
		{Weekly, "2026-10-19", "2026-10-25", "this week"},
		{Weekly, "2026-10-12", "2026-10-18", "last week"},
		{Weekly, "2026-10-05", "2026-10-11", "in the week of 5 Oct"},
		{Monthly, "2026-09-21", "2026-10-18", "over the 28 days to 18 Oct"},
		{LongTerm, "2026-07-20", "2026-10-18", "over the 13 weeks to 18 Oct"},
	}
	for _, c := range cases {
		if got := PeriodPhrase(c.kind, c.start, c.end, today); got != c.want {
			t.Errorf("%s %s: got %q want %q", c.kind, c.start, got, c.want)
		}
	}
	in := Insight{PeriodKind: Weekly, PeriodStart: "2026-10-12", PeriodEnd: "2026-10-18", Title: "Only 1 session {period}"}
	in.render(today)
	if in.Title != "Only 1 session last week" {
		t.Fatal(in.Title)
	}
}
