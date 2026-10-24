package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ritm/internal/clock"
	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

// Period kinds. Monthly and long-term windows are aligned 28- and 91-day blocks counted
// from Monday 1970-01-05, so re-running the engine on later days yields the same periods.
const (
	Daily    = "daily"
	Weekly   = "weekly"
	Monthly  = "monthly"
	LongTerm = "long_term"
)

const (
	Positive  = "positive"
	Neutral   = "neutral"
	Attention = "attention"
)

// History depth regenerated on every run.
var historyPeriods = map[string]int{Daily: 30, Weekly: 12, Monthly: 6, LongTerm: 4}

var epochMonday = days.MustParse("1970-01-05")

type Period struct {
	Kind string
	Cur  days.Range
}

func blockLen(kind string) int {
	switch kind {
	case Weekly:
		return 7
	case Monthly:
		return 28
	case LongTerm:
		return 91
	}
	return 1
}

// periodsEndingBy returns the last n complete periods of kind that end on or before anchor,
// most recent first.
func periodsEndingBy(kind string, anchor days.Day, n int) []Period {
	if kind == Daily {
		out := make([]Period, n)
		for i := range out {
			d := anchor.Add(-i)
			out[i] = Period{Kind: Daily, Cur: days.Range{From: d, To: d}}
		}
		return out
	}
	l := days.Day(blockLen(kind))
	idx := (anchor - epochMonday + 1) / l // blocks fully completed by anchor
	var out []Period
	for i := 0; i < n; i++ {
		start := epochMonday + (idx-1-days.Day(i))*l
		out = append(out, Period{Kind: kind, Cur: days.Range{From: start, To: start + l - 1}})
	}
	return out
}

// Prev returns the period of the same length immediately before.
func (p Period) Prev(n int) days.Range {
	l := days.Day(p.Cur.Len())
	return days.Range{From: p.Cur.From - l*days.Day(n), To: p.Cur.To - l*days.Day(n)}
}

type Insight struct {
	ID          int64          `json:"id"`
	RuleID      string         `json:"rule_id"`
	Category    string         `json:"category" enum:"activity,heart,training,recovery"`
	Polarity    string         `json:"polarity" enum:"positive,neutral,attention"`
	PeriodKind  string         `json:"period_kind" enum:"daily,weekly,monthly,long_term"`
	PeriodStart string         `json:"period_start"`
	PeriodEnd   string         `json:"period_end"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Metric      string         `json:"metric,omitempty"`
	Current     *float64       `json:"current_value"`
	Baseline    *float64       `json:"baseline_value"`
	DeltaPct    *float64       `json:"delta_pct"`
	Evidence    map[string]any `json:"evidence"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Rule struct {
	ID       string
	Category string
	Kind     string
	Eval     func(c *Ctx, p Period) *Insight
}

// Ctx gives rules access to data and resolved settings.
type Ctx struct {
	Data *Data
	Res  *derive.Resolved
}

type Engine struct {
	db    *store.DB
	zone  *days.Zone
	clock clock.Clock
}

func NewEngine(db *store.DB, zone *days.Zone, clk clock.Clock) *Engine {
	return &Engine{db: db, zone: zone, clock: clk}
}

// Generate evaluates all rules for the latest periods plus a short history and stores the result,
// replacing previously generated insights for the same periods.
func (e *Engine) Generate(ctx context.Context, _ days.Day, res *derive.Resolved) error {
	today := e.zone.Today(e.clock.Now())
	data, err := Load(ctx, e.db.W, res.SessionTypesEffective, -1, today)
	if err != nil {
		return err
	}
	out := Evaluate(data, res)
	now := e.clock.Now().UnixMilli()

	return store.InTx(ctx, e.db.W, func(tx *sql.Tx) error {
		// Generated insights fully replace stored ones within the evaluated horizon.
		horizon := map[string]string{}
		if data.Anchor >= 0 {
			for kind, n := range historyPeriods {
				ps := periodsEndingBy(kind, data.Anchor, n)
				horizon[kind] = ps[len(ps)-1].Cur.From.String()
			}
		}
		keep := map[string]bool{}
		for _, in := range out {
			ev, _ := json.Marshal(in.Evidence)
			if _, err := tx.ExecContext(ctx, `INSERT INTO insights (rule_id, category, polarity, period_kind, period_start, period_end,
				title, body, metric, current_value, baseline_value, delta_pct, evidence_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (rule_id, period_kind, period_start) DO UPDATE SET category = excluded.category,
					polarity = excluded.polarity, period_end = excluded.period_end, title = excluded.title, body = excluded.body,
					metric = excluded.metric, current_value = excluded.current_value, baseline_value = excluded.baseline_value,
					delta_pct = excluded.delta_pct, evidence_json = excluded.evidence_json`,
				in.RuleID, in.Category, in.Polarity, in.PeriodKind, in.PeriodStart, in.PeriodEnd, in.Title, in.Body,
				nullStr(in.Metric), in.Current, in.Baseline, in.DeltaPct, string(ev), now); err != nil {
				return err
			}
			keep[in.RuleID+"|"+in.PeriodKind+"|"+in.PeriodStart] = true
		}
		rows, err := tx.QueryContext(ctx, "SELECT id, rule_id, period_kind, period_start FROM insights")
		if err != nil {
			return err
		}
		var stale []int64
		for rows.Next() {
			var id int64
			var rule, kind, start string
			if err := rows.Scan(&id, &rule, &kind, &start); err != nil {
				rows.Close()
				return err
			}
			if h, ok := horizon[kind]; ok && start >= h && !keep[rule+"|"+kind+"|"+start] {
				stale = append(stale, id)
			}
			if _, ok := horizon[kind]; !ok && data.Anchor < 0 {
				stale = append(stale, id)
			}
		}
		rows.Close()
		for _, id := range stale {
			if _, err := tx.ExecContext(ctx, "DELETE FROM insights WHERE id = ?", id); err != nil {
				return err
			}
		}
		return store.BumpDataVersion(ctx, tx)
	})
}

// Evaluate runs every rule over the latest periods and history.
func Evaluate(data *Data, res *derive.Resolved) []*Insight {
	if data.Anchor < 0 {
		return nil
	}
	c := &Ctx{Data: data, Res: res}
	var out []*Insight
	seen := map[string]bool{}
	for _, r := range Rules() {
		for _, p := range periodsEndingBy(r.Kind, data.Anchor, historyPeriods[r.Kind]) {
			if p.Cur.From < data.First {
				break
			}
			in := r.Eval(c, p)
			if in == nil {
				continue
			}
			in.RuleID, in.Category, in.PeriodKind = r.ID, r.Category, p.Kind
			if in.PeriodStart == "" {
				in.PeriodStart, in.PeriodEnd = p.Cur.From.String(), p.Cur.To.String()
			}
			// Rules may widen the period (e.g. a multi-day spike); the most recent evaluation wins.
			key := in.RuleID + "|" + in.PeriodKind + "|" + in.PeriodStart
			if seen[key] {
				continue
			}
			seen[key] = true
			if in.Evidence == nil {
				in.Evidence = map[string]any{}
			}
			in.Evidence["period"] = map[string]string{"from": in.PeriodStart, "to": in.PeriodEnd}
			out = append(out, in)
		}
	}
	return out
}

// PeriodToken in stored titles/bodies is replaced at read time with a phrase relative to the
// reader's today ("this week", "last week", "in the week of 12 Oct", …), so text stays correct
// as days pass between imports.
const PeriodToken = "{period}"

// PeriodPhrase describes an insight period relative to today.
func PeriodPhrase(kind, start, end string, today days.Day) string {
	s, err1 := days.Parse(start)
	e, err2 := days.Parse(end)
	if err1 != nil || err2 != nil {
		return ""
	}
	label := func(d days.Day) string { return d.Time(time.UTC).Format("2 Jan") }
	switch kind {
	case Weekly:
		switch {
		case today > 0 && s <= today && today <= e:
			return "this week"
		case today > 0 && e == today.ISOWeekStart()-1:
			return "last week"
		}
		return "in the week of " + label(s)
	case Monthly:
		return "over the 28 days to " + label(e)
	case LongTerm:
		return "over the 13 weeks to " + label(e)
	}
	return ""
}

func (in *Insight) render(today days.Day) {
	p := PeriodPhrase(in.PeriodKind, in.PeriodStart, in.PeriodEnd, today)
	in.Title = strings.ReplaceAll(in.Title, PeriodToken, p)
	in.Body = strings.ReplaceAll(in.Body, PeriodToken, p)
}

// Filter selects stored insights.
type Filter struct {
	Today      days.Day // renders period phrases relative to this day (0 = absolute dates)
	From, To   string   // period_end range, inclusive
	Category   string
	Polarity   string
	PeriodKind string
	LatestOnly bool // only the most recent period per rule
	Limit      int
}

func List(ctx context.Context, q store.Querier, f Filter) ([]Insight, error) {
	var where []string
	var args []any
	// periods overlapping [From, To]
	if f.From != "" {
		where = append(where, "period_end >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "period_start <= ?")
		args = append(args, f.To)
	}
	for col, v := range map[string]string{"category": f.Category, "polarity": f.Polarity, "period_kind": f.PeriodKind} {
		if v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	if f.LatestOnly {
		where = append(where, `period_end = (SELECT max(i2.period_end) FROM insights i2 WHERE i2.rule_id = insights.rule_id)`)
	}
	sqlq := `SELECT id, rule_id, category, polarity, period_kind, period_start, period_end, title, body, ifnull(metric, ''),
		current_value, baseline_value, delta_pct, ifnull(evidence_json, '{}'), created_at FROM insights`
	if len(where) > 0 {
		sqlq += " WHERE " + strings.Join(where, " AND ")
	}
	// attention first, then most recent
	sqlq += ` ORDER BY period_end DESC, CASE polarity WHEN 'attention' THEN 0 WHEN 'positive' THEN 1 ELSE 2 END,
		CASE period_kind WHEN 'daily' THEN 0 WHEN 'weekly' THEN 1 WHEN 'monthly' THEN 2 ELSE 3 END, abs(ifnull(delta_pct, 0)) DESC, id`
	if f.Limit > 0 {
		sqlq += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := q.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Insight{}
	for rows.Next() {
		var in Insight
		var cur, base, delta sql.NullFloat64
		var ev string
		var created int64
		if err := rows.Scan(&in.ID, &in.RuleID, &in.Category, &in.Polarity, &in.PeriodKind, &in.PeriodStart, &in.PeriodEnd,
			&in.Title, &in.Body, &in.Metric, &cur, &base, &delta, &ev, &created); err != nil {
			return nil, err
		}
		in.Current, in.Baseline, in.DeltaPct = ptr(cur), ptr(base), ptr(delta)
		_ = json.Unmarshal([]byte(ev), &in.Evidence)
		in.CreatedAt = time.UnixMilli(created)
		in.render(f.Today)
		out = append(out, in)
	}
	return out, rows.Err()
}

func ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fp(v float64) *float64 { return mathx.RoundPtr(v, 2) }
