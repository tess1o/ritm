<script setup lang="ts">
import { computed, ref } from 'vue'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import DeltaBadge from '../components/DeltaBadge.vue'
import Icon from '../components/Icon.vue'
import InsightItem from '../components/InsightItem.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { get, qs } from '../lib/api'
import { addDays, num, range, today } from '../lib/fmt'
import type { DailyActivity, DailyHeart, Insight, InsightList, LoadDay, SleepNight, Weekly } from '../lib/types'
import { axisTooltip, color, dayAxis, grid, valueAxis } from '../charts/helpers'

setPageMeta(() => ({ subtitle: 'Comparisons with your own baseline · informational, not medical advice' }))
const category = ref('')
const polarity = ref('')
const { range: period, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => period.value.to < today())
const list = useAsync(
  (s) =>
    get<InsightList>(
      `/insights${qs({ period: `${period.value.from}..${period.value.to}`, category: category.value, polarity: polarity.value, limit: 300 })}`,
      s,
    ),
  [period, category, polarity],
)

const groups = computed(() => {
  const m = new Map<string, Insight[]>()
  for (const i of list.data.value?.items ?? []) {
    const k = i.period_end
    m.set(k, [...(m.get(k) ?? []), i])
  }
  return [...m.entries()]
})

// Evidence chart: the metric's values over the insight period and the stretch before it, with the
// baseline as a dashed line. Each source says which API it reads and how to pick the values.
type Row = { day: string; v: number | null }
type Source = {
  url: string // API path without the leading slash
  rows: (r: never) => Row[]
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
const hasChart = (i: Insight) => !!i.metric && (!!metricSource[i.metric] || (!!groupCharts[i.metric] && i.current_value !== null && i.baseline_value !== null))
const open = ref<number | null>(null)
const evidence = ref<{ id: number; series: Row[]; unit: string } | null>(null)
const evidenceLoading = ref(false)
async function toggle(i: Insight) {
  if (open.value === i.id) {
    open.value = null
    return
  }
  open.value = i.id
  const src = i.metric ? metricSource[i.metric] : undefined
  evidence.value = null
  if (!src) return
  evidenceLoading.value = true
  const len = Math.round((new Date(i.period_end).getTime() - new Date(i.period_start).getTime()) / 86400000) + 1
  const from = addDays(i.period_start, -Math.max(28, len * 2))
  try {
    const r = await get<never>(`/${src.url}${qs({ from, to: i.period_end, hours: false })}`)
    evidence.value = { id: i.id, unit: src.unit, series: src.rows(r) }
  } finally {
    evidenceLoading.value = false
  }
}
function baselineOf(i: Insight) {
  const src = i.metric ? metricSource[i.metric] : undefined
  return src?.baseline ? src.baseline(i) : null
}
function evidenceOption(i: Insight) {
  const group = i.metric ? groupCharts[i.metric] : undefined
  if (group) {
    return {
      grid: grid({ top: 16 }),
      tooltip: axisTooltip(),
      xAxis: { type: 'category', data: group.labels, axisTick: { show: false } },
      yAxis: valueAxis(group.unit, { scale: true }),
      series: [{ type: 'bar', barMaxWidth: 48, data: [
        { value: i.current_value, itemStyle: { color: color('--s-blue') } },
        { value: i.baseline_value, itemStyle: { color: color('--c-context') } },
      ] }],
    }
  }
  const s = evidence.value?.series ?? []
  const base = baselineOf(i)
  return {
    grid: grid({ top: 16 }),
    tooltip: axisTooltip(),
    xAxis: dayAxis(s.map((x) => x.day)),
    yAxis: valueAxis(evidence.value?.unit, { scale: true }),
    series: [
      {
        type: 'bar',
        name: evidence.value?.unit,
        data: s.map((x) => ({ value: x.v, itemStyle: { color: x.day >= i.period_start ? color('--s-blue') : color('--c-context') } })),
        barMaxWidth: 14,
        markLine:
          base !== null
            ? { silent: true, symbol: 'none', lineStyle: { color: color('--muted'), type: 'dashed' }, label: { formatter: 'baseline', color: color('--muted') }, data: [{ yAxis: base }] }
            : undefined,
      },
    ],
  }
}
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
const evidenceRows = (i: Insight) =>
  Object.entries(i.evidence ?? {})
    .filter(([k, v]) => k !== 'period' && k !== 'unit' && v !== null && v !== undefined)
    .map(([k, v]) => ({ label: pretty(k), value: value(v) }))
const unitOf = (i: Insight) => (typeof i.evidence?.unit === 'string' ? (i.evidence.unit as string) : '')
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <Teleport defer to="#topbar-actions">
      <select v-model="category" class="input filter" aria-label="Category">
        <option value="">All categories</option>
        <option value="activity">Activity</option>
        <option value="heart">Heart</option>
        <option value="training">Training</option>
        <option value="recovery">Recovery</option>
      </select>
      <select v-model="polarity" class="input filter" aria-label="Polarity">
        <option value="">Any polarity</option>
        <option value="positive">Positive</option>
        <option value="neutral">Neutral</option>
        <option value="attention">Attention</option>
      </select>
    </Teleport>

    <StateBlock :loading="list.loading.value" :error="list.error.value" :empty="!groups.length" :height="300"
      empty-text="No insights for this period. They need a few weeks of data, and only notable changes are reported.">
      <Card v-for="[end, items] in groups" :key="end" :title="`Periods ending ${end}`">
        <div>
          <InsightItem v-for="i in items" :key="i.id" :insight="i">
            <button class="ev-toggle" type="button" :aria-expanded="open === i.id" :aria-controls="`ev-${i.id}`" @click="toggle(i)">
              <Icon name="right" :size="16" class="chev" :class="{ open: open === i.id }" />
              {{ open === i.id ? 'Hide evidence' : 'Show evidence' }}
            </button>
            <section v-if="open === i.id" :id="`ev-${i.id}`" class="ev-panel" aria-label="Evidence">
              <div v-if="i.current_value !== null" class="ev-compare">
                <div class="ev-stat">
                  <span class="ev-label">This period</span>
                  <span class="ev-value tnum">{{ num(i.current_value, i.current_value < 100 ? 1 : 0) }}<small v-if="unitOf(i)"> {{ unitOf(i) }}</small></span>
                  <span class="ev-sub">{{ range(i.period_start, i.period_end) }}</span>
                </div>
                <div v-if="i.baseline_value !== null" class="ev-stat">
                  <span class="ev-label">Baseline</span>
                  <span class="ev-value tnum muted-value">{{ num(i.baseline_value, i.baseline_value < 100 ? 1 : 0) }}<small v-if="unitOf(i)"> {{ unitOf(i) }}</small></span>
                  <span class="ev-sub">your usual level</span>
                </div>
                <div v-if="i.delta_pct !== null" class="ev-stat">
                  <span class="ev-label">Change</span>
                  <span class="ev-value"><DeltaBadge :delta="i.delta_pct" :neutral="true" /></span>
                </div>
              </div>

              <div v-if="hasChart(i)" class="ev-chart">
                <Chart v-if="groupCharts[i.metric!]" :option="evidenceOption(i)" :height="170" :label="`Comparison behind: ${i.title}`" />
                <div v-else-if="evidenceLoading && evidence?.id !== i.id" class="ev-skeleton" />
                <template v-else-if="evidence?.id === i.id && evidence.series.length">
                  <Chart :option="evidenceOption(i)" :height="170" :label="`Daily values behind: ${i.title}`" />
                  <div class="ev-legend small">
                    <span><i class="sw cur" /> Insight period</span>
                    <span><i class="sw prev" /> Before</span>
                    <span v-if="baselineOf(i) !== null"><i class="sw base" /> Baseline</span>
                  </div>
                </template>
              </div>

              <dl v-if="evidenceRows(i).length" class="ev-grid">
                <div v-for="r in evidenceRows(i)" :key="r.label" class="ev-item">
                  <dt>{{ r.label }}</dt>
                  <dd class="tnum">{{ r.value }}</dd>
                </div>
              </dl>
            </section>
          </InsightItem>
        </div>
      </Card>
    </StateBlock>
  </div>
</template>

<style scoped>
.filter {
  width: auto;
  min-height: 44px;
}
.ev-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  align-self: flex-start;
  min-height: 36px;
  margin-top: 4px;
  padding: 0 10px 0 6px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: transparent;
  color: var(--text-2);
  font-size: 0.82rem;
  font-weight: 560;
  cursor: pointer;
}
.ev-toggle:hover,
.ev-toggle[aria-expanded='true'] {
  border-color: var(--border-strong);
  color: var(--text);
  background: var(--elevated);
}
.chev {
  transition: transform 0.15s ease;
}
.chev.open {
  transform: rotate(90deg);
}
.ev-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin-top: 8px;
  padding: 14px;
  border-radius: var(--radius-sm);
  background: var(--elevated);
  border: 1px solid var(--border);
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
  background: var(--surface);
  border: 1px solid var(--border);
}
.ev-label {
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
  background: var(--surface);
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
</style>
