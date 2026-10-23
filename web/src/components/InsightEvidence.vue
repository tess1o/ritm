<script setup lang="ts">
// Evidence behind one insight: this period vs baseline, the metric's values over the period and the
// stretch before it, the rule's supporting numbers, and earlier periods of a collapsed run.
import { computed } from 'vue'
import Chart from './Chart.vue'
import DeltaBadge from './DeltaBadge.vue'
import Icon from './Icon.vue'
import { useAsync } from '../composables/useAsync'
import { get, qs } from '../lib/api'
import { addDays, num, range } from '../lib/fmt'
import { periodLabel } from '../lib/insights'
import type { DailyActivity, DailyHeart, Insight, LoadDay, SleepNight, Weekly } from '../lib/types'
import { axisTooltip, color, dayAxis, grid, valueAxis } from '../charts/helpers'

const props = defineProps<{ insight: Insight; repeats?: Insight[]; heading?: boolean }>()
const emit = defineEmits<{ close: [] }>()

// Each source says which API it reads and how to pick the values.
type Point = { day: string; v: number | null }
type Source = {
  url: string // API path without the leading slash
  rows: (r: never) => Point[]
  unit: string
  baseline?: (i: Insight) => number | null // in chart units; omitted = no line
  label?: string // what one bar is
}
const asIs = (i: Insight) => i.baseline_value
function daily<T extends { day: string }>(pick: (d: T) => number | null) {
  // Collections have items; training load lists its days.
  return (r: { items?: T[]; days?: T[] }) => (r.items ?? r.days ?? []).map((d) => ({ day: d.day, v: pick(d) }))
}
const watch = (f: (d: DailyActivity) => number | null) => daily<DailyActivity>((d) => (d.has_watch_data ? f(d) : null))
const sessions = (f: (s: Weekly['sessions'][number]) => number | null) => (r: Weekly) => r.sessions.map((s) => ({ day: s.day, v: f(s) }))
const metricSource: Record<string, Source> = {
  steps: { url: 'activity/daily', rows: watch((d) => d.steps), unit: 'steps', baseline: asIs },
  distance_m: { url: 'activity/daily', rows: watch((d) => (d.distance_m === null ? null : d.distance_m / 1000)), unit: 'km',
    baseline: (i) => (i.baseline_value === null ? null : i.baseline_value / 1000) },
  active_kcal: { url: 'activity/daily', rows: watch((d) => d.active_kcal), unit: 'kcal', baseline: asIs },
  step_goal_days: { url: 'activity/daily', rows: watch((d) => d.steps), unit: 'steps' },
  longest_sedentary_h: { url: 'activity/daily', rows: daily<DailyActivity>((d) => d.longest_sedentary_h), unit: 'h', baseline: asIs },
  steps_weekend_ratio: { url: 'activity/daily', rows: watch((d) => d.steps), unit: 'steps' },
  steps_non_gym_days: { url: 'activity/daily', rows: watch((d) => d.steps), unit: 'steps' },
  move_ring_days: { url: 'activity/daily', unit: '% of goal', label: 'Move ring',
    rows: daily<DailyActivity>((d) => (d.rings.move_kcal !== null && d.rings.move_goal_kcal ? Math.round((d.rings.move_kcal / d.rings.move_goal_kcal) * 100) : null)),
    baseline: () => 100 },
  resting_hr: { url: 'heart/daily', rows: daily<DailyHeart>((d) => d.resting_hr_bpm), unit: 'bpm', baseline: asIs },
  walking_hr: { url: 'heart/daily', rows: daily<DailyHeart>((d) => d.walking_hr_bpm), unit: 'bpm', baseline: asIs },
  hrv_sdnn: { url: 'heart/daily', rows: daily<DailyHeart>((d) => d.hrv_sdnn_ms), unit: 'ms', baseline: asIs },
  vo2max: { url: 'heart/daily', rows: daily<DailyHeart>((d) => d.vo2max_ml_kg_min), unit: 'mL/kg/min', baseline: asIs },
  heart_events: { url: 'heart/daily', rows: daily<DailyHeart>((d) => d.heart_events || null), unit: 'events' },
  asleep_min: { url: 'sleep/nights', rows: daily<SleepNight>((d) => (d.asleep_min === null ? null : d.asleep_min / 60)), unit: 'h',
    baseline: (i) => (i.baseline_value === null ? null : i.baseline_value / 60) },
  sessions: { url: 'training/weekly', unit: 'sessions', label: 'week', baseline: asIs,
    rows: (r: Weekly) => r.weeks.map((w) => ({ day: w.week_start, v: w.sessions })) },
  training_hours: { url: 'training/weekly', unit: 'h', label: 'session', rows: sessions((x) => (x.duration_s === null ? null : x.duration_s / 3600)) },
  session_duration_min: { url: 'training/weekly', unit: 'min', label: 'session', baseline: asIs,
    rows: sessions((x) => (x.duration_s === null ? null : Math.round(x.duration_s / 60))) },
  pct_z3_plus: { url: 'training/weekly', unit: '% in zone 3+', label: 'session', baseline: asIs, rows: sessions((x) => x.pct_z3_plus) },
  session_avg_hr: { url: 'training/weekly', unit: 'bpm', label: 'session', baseline: asIs, rows: sessions((x) => x.avg_hr_bpm) },
  acwr: { url: 'training/load', unit: 'ACWR', rows: daily<LoadDay>((d) => d.acwr) },
  tsb: { url: 'training/load', unit: 'TSB', rows: daily<LoadDay>((d) => d.tsb) },
}
// Insights that compare two groups of days get a two-bar chart from their own values.
const groupCharts: Record<string, { labels: [string, string]; unit: string }> = {
  next_day_resting_hr_delta: { labels: ['After training', 'After rest'], unit: 'bpm' },
  hrv_after_short_sleep: { labels: ['After nights < 6 h', 'Other nights'], unit: 'ms' },
}

const src = computed(() => (props.insight.metric ? metricSource[props.insight.metric] : undefined))
const group = computed(() => {
  const i = props.insight
  const g = i.metric ? groupCharts[i.metric] : undefined
  return g && i.current_value !== null && i.baseline_value !== null ? g : undefined
})
const series = useAsync(async (signal) => {
  const i = props.insight
  const s = src.value
  if (!s) return null
  const len = Math.round((new Date(i.period_end).getTime() - new Date(i.period_start).getTime()) / 86400000) + 1
  const from = addDays(i.period_start, -Math.max(28, len * 2))
  return s.rows(await get<never>(`/${s.url}${qs({ from, to: i.period_end, hours: false })}`, signal))
}, [() => props.insight.id])
const baseline = computed(() => (src.value?.baseline ? src.value.baseline(props.insight) : null))

const option = computed(() => {
  const i = props.insight
  const g = group.value
  if (g) {
    return {
      grid: grid({ top: 16 }),
      tooltip: axisTooltip(),
      xAxis: { type: 'category', data: g.labels, axisTick: { show: false } },
      yAxis: valueAxis(g.unit, { scale: true }),
      series: [{ type: 'bar', barMaxWidth: 48, data: [
        { value: i.current_value, itemStyle: { color: color('--s-blue') } },
        { value: i.baseline_value, itemStyle: { color: color('--c-context') } },
      ] }],
    }
  }
  const s = series.data.value ?? []
  const base = baseline.value
  return {
    grid: grid({ top: 16 }),
    tooltip: axisTooltip(),
    xAxis: dayAxis(s.map((x) => x.day)),
    yAxis: valueAxis(src.value?.unit, { scale: true }),
    series: [
      {
        type: 'bar',
        name: src.value?.unit,
        data: s.map((x) => ({ value: x.v, itemStyle: { color: x.day >= i.period_start ? color('--s-blue') : color('--c-context') } })),
        barMaxWidth: 14,
        markLine:
          base !== null
            ? { silent: true, symbol: 'none', lineStyle: { color: color('--muted'), type: 'dashed' }, label: { formatter: 'baseline', position: 'insideEndTop', color: color('--muted') }, data: [{ yAxis: base }] }
            : undefined,
      },
    ],
  }
})

// Human labels for evidence keys; unknown keys are prettified.
const LABELS: Record<string, string> = {
  days_with_data: 'Days with data',
  baseline_days: 'Baseline days',
  baseline_weeks: 'Baseline weeks',
  baseline_weekly_sd: 'Week-to-week variation (SD)',
  baseline_weekly_sd_min: 'Week-to-week variation (SD, min)',
  previous_period: 'Compared with',
  baseline_period: 'Baseline period',
  median_90d: '90-day median',
  goal_steps: 'Step goal',
  streak_weeks: 'Streak (weeks)',
  target: 'Target',
  threshold_bpm: 'Threshold (bpm)',
  sessions: 'Sessions',
  previous_sessions: 'Sessions before',
  baseline_sessions: 'Baseline sessions',
  median_duration_min: 'Typical session (min)',
  days_after_training: 'Days after training',
  days_after_rest: 'Days after rest',
  hrv_after_training_ms: 'HRV after training (ms)',
  hrv_after_rest_ms: 'HRV after rest (ms)',
  short_nights: 'Nights under 6 h',
  other_nights: 'Other nights',
  nights: 'Nights',
  unit: 'Unit',
}
function pretty(k: string) {
  if (LABELS[k]) return LABELS[k]
  const t = k.replace(/_/g, ' ')
  return t.charAt(0).toUpperCase() + t.slice(1)
}
function value(v: unknown): string {
  if (v === null || v === undefined) return '–'
  if (typeof v === 'number') return num(v, Number.isInteger(v) ? 0 : 1)
  if (typeof v === 'object') {
    const o = v as Record<string, string>
    if (o.from && o.to) return range(o.from, o.to)
    return Object.values(o).join(' · ')
  }
  return String(v)
}
const rows = computed(() =>
  Object.entries(props.insight.evidence ?? {})
    .filter(([k, v]) => k !== 'period' && k !== 'unit' && v !== null && v !== undefined)
    .map(([k, v]) => ({ label: pretty(k), value: value(v) })),
)
const unit = computed(() => (typeof props.insight.evidence?.unit === 'string' ? (props.insight.evidence.unit as string) : ''))
const fmtValue = (v: number) => num(v, Math.abs(v) < 100 ? 1 : 0)

const POLARITY = { positive: 'Positive', neutral: 'Note', attention: 'Attention' } as const
const CATEGORY_ICON = { activity: 'activity', heart: 'heart', training: 'dumbbell', recovery: 'moon' } as const
</script>

<template>
  <section class="evidence" :aria-label="`Evidence for: ${insight.title}`">
    <header v-if="heading" class="ev-head">
      <div class="ev-chips">
        <span class="chip" :class="insight.polarity">{{ POLARITY[insight.polarity] }}</span>
        <span class="chip cat"><Icon :name="CATEGORY_ICON[insight.category]" :size="13" />{{ insight.category }}</span>
        <span class="muted small">{{ periodLabel(insight) }}</span>
        <button class="close" type="button" aria-label="Close evidence" @click="emit('close')"><Icon name="x" :size="16" /></button>
      </div>
      <h3>{{ insight.title }}</h3>
      <p class="text-2">{{ insight.body }}</p>
    </header>

    <div v-if="insight.current_value !== null" class="ev-compare">
      <div class="ev-stat">
        <span class="ev-label">This period</span>
        <span class="ev-value tnum">{{ fmtValue(insight.current_value) }}<small v-if="unit"> {{ unit }}</small></span>
        <span class="ev-sub">{{ range(insight.period_start, insight.period_end) }}</span>
      </div>
      <div v-if="insight.baseline_value !== null" class="ev-stat">
        <span class="ev-label">Baseline</span>
        <span class="ev-value tnum muted-value">{{ fmtValue(insight.baseline_value) }}<small v-if="unit"> {{ unit }}</small></span>
        <span class="ev-sub">your usual level</span>
      </div>
      <div v-if="insight.delta_pct !== null" class="ev-stat">
        <span class="ev-label">Change</span>
        <span class="ev-value"><DeltaBadge :delta="insight.delta_pct" :neutral="true" /></span>
      </div>
    </div>

    <div v-if="group || src" class="ev-chart">
      <Chart v-if="group" :option="option" :height="170" :label="`Comparison behind: ${insight.title}`" />
      <div v-else-if="series.loading.value" class="ev-skeleton" />
      <template v-else-if="series.data.value?.length">
        <Chart :option="option" :height="170" :label="`Daily values behind: ${insight.title}`" />
        <div class="ev-legend small">
          <span><i class="sw cur" /> Insight period</span>
          <span><i class="sw prev" /> Before</span>
          <span v-if="baseline !== null"><i class="sw base" /> Baseline</span>
        </div>
      </template>
    </div>

    <dl v-if="rows.length" class="ev-grid">
      <div v-for="r in rows" :key="r.label" class="ev-item">
        <dt>{{ r.label }}</dt>
        <dd class="tnum">{{ r.value }}</dd>
      </div>
    </dl>

    <div v-if="repeats?.length" class="ev-history">
      <h4 class="ev-label">Earlier in this run</h4>
      <ul>
        <li v-for="r in repeats" :key="r.id">
          <span class="muted tnum">{{ range(r.period_start, r.period_end) }}</span>
          <span>{{ r.title }}</span>
        </li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.evidence {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}
.ev-head {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ev-head h3 {
  margin: 4px 0 0;
  font-size: 1.05rem;
}
.ev-head p {
  margin: 0;
}
.ev-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.chip.cat {
  text-transform: capitalize;
}
.close {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  margin-left: auto;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.close:hover {
  background: var(--elevated);
  color: var(--text);
}
.ev-compare {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 10px;
}
.ev-stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--ev-tile, var(--elevated));
  border: 1px solid var(--border);
}
.ev-label {
  margin: 0;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--muted);
}
.ev-value {
  font-size: 1.25rem;
  font-weight: 650;
  color: var(--text);
}
.ev-value small {
  margin-left: 3px;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--muted);
}
.muted-value {
  color: var(--text-2);
}
.ev-sub {
  font-size: 0.78rem;
  color: var(--muted);
}
.ev-chart {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ev-skeleton {
  height: 170px;
  border-radius: var(--radius-sm);
  background: var(--elevated);
}
.ev-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  color: var(--text-2);
}
.ev-legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.sw {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 3px;
}
.sw.cur {
  background: var(--s-blue);
}
.sw.prev {
  background: var(--c-context);
}
.sw.base {
  width: 14px;
  height: 0;
  border-radius: 0;
  border-top: 2px dashed var(--muted);
}
.ev-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 1px;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--border);
}
.ev-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 12px;
  background: var(--surface);
}
dt {
  font-size: 0.75rem;
  color: var(--muted);
}
dd {
  margin: 0;
  font-size: 0.92rem;
  font-weight: 560;
  color: var(--text);
}
.ev-history ul {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
  font-size: 0.88rem;
}
.ev-history li {
  display: grid;
  grid-template-columns: 7.5em minmax(0, 1fr);
  gap: 10px;
}
</style>
