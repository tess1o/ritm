// Grouping of the Insights list: weekly and daily insights by calendar week, 28-day and 13-week
// ones as trends, and runs of the same rule in consecutive periods collapsed into one row.
import { addDays, parseDay, range, shortDay } from './fmt'
import type { Insight, Polarity } from './types'

/** An insight shown as one row; earlier consecutive periods of the same rule ride along. */
export type Row = { insight: Insight; repeats: Insight[] }
export type Group = { key: string; title: string; sub: string; rows: Row[] }

const BLOCK: Record<Insight['period_kind'], number> = { daily: 1, weekly: 7, monthly: 28, long_term: 91 }
const POLARITY_RANK: Record<Polarity, number> = { attention: 0, positive: 1, neutral: 2 }
const KIND_RANK: Record<Insight['period_kind'], number> = { daily: 0, weekly: 1, monthly: 2, long_term: 3 }

const daysBetween = (a: string, b: string) => Math.round((parseDay(b).getTime() - parseDay(a).getTime()) / 86400000)

export function mondayOf(day: string): string {
  return addDays(day, -((parseDay(day).getDay() + 6) % 7))
}

/** Collapses runs of the same rule and polarity in back-to-back periods into the newest one. */
export function collapse(items: Insight[]): Row[] {
  const byRule = new Map<string, Insight[]>()
  for (const i of items) {
    const k = `${i.rule_id}|${i.period_kind}|${i.polarity}`
    byRule.set(k, [...(byRule.get(k) ?? []), i])
  }
  const rows: Row[] = []
  for (const list of byRule.values()) {
    list.sort((a, b) => b.period_start.localeCompare(a.period_start))
    let head: Row | null = null
    let last: Insight | null = null // oldest insight of the current run
    for (const i of list) {
      if (head && last && i.period_kind !== 'daily' && daysBetween(i.period_start, last.period_start) === BLOCK[i.period_kind]) {
        head.repeats.push(i)
      } else {
        head = { insight: i, repeats: [] }
        rows.push(head)
      }
      last = i
    }
  }
  return rows
}

/** "3 weeks in a row" for a collapsed run, else null. */
export function streakLabel(r: Row): string | null {
  const n = r.repeats.length + 1
  if (n < 2) return null
  return r.insight.period_kind === 'weekly' ? `${n} weeks in a row` : `${n} periods in a row`
}

function weekTitle(monday: string, today: string): string {
  const cur = mondayOf(today)
  if (monday === cur) return 'This week'
  if (monday === addDays(cur, -7)) return 'Last week'
  return `Week of ${shortDay(monday)}`
}

const compareRows = (a: Row, b: Row) =>
  POLARITY_RANK[a.insight.polarity] - POLARITY_RANK[b.insight.polarity] ||
  KIND_RANK[a.insight.period_kind] - KIND_RANK[b.insight.period_kind] ||
  Math.abs(b.insight.delta_pct ?? 0) - Math.abs(a.insight.delta_pct ?? 0) ||
  a.insight.id - b.insight.id

/** Groups rows newest first: weeks ("Last week · 12–18 Oct") and trends ("4-week trends"). */
export function group(rows: Row[], today: string): Group[] {
  const m = new Map<string, Group & { end: string; order: number }>()
  for (const r of rows) {
    const i = r.insight
    const week = i.period_kind === 'daily' || i.period_kind === 'weekly'
    const monday = mondayOf(i.period_end)
    const key = week ? `w|${monday}` : `${i.period_kind}|${i.period_end}`
    let g = m.get(key)
    if (!g) {
      g = week
        ? { key, title: weekTitle(monday, today), sub: range(monday, addDays(monday, 6)), rows: [], end: addDays(monday, 6), order: 0 }
        : {
            key,
            title: i.period_kind === 'monthly' ? '4-week trends' : '13-week trends',
            sub: range(i.period_start, i.period_end),
            rows: [],
            end: i.period_end,
            order: KIND_RANK[i.period_kind],
          }
      m.set(key, g)
    }
    g.rows.push(r)
  }
  return [...m.values()]
    .sort((a, b) => b.end.localeCompare(a.end) || a.order - b.order)
    .map(({ key, title, sub, rows }) => ({ key, title, sub, rows: rows.sort(compareRows) }))
}

/** One-line description of an insight's period, e.g. "Week · 12–18 Oct". */
export function periodLabel(i: Insight): string {
  const kind = { daily: 'Daily', weekly: 'Week', monthly: '28 days', long_term: '13 weeks' }[i.period_kind]
  return `${kind} · ${range(i.period_start, i.period_end)}`
}
