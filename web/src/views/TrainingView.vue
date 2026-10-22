<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import DataTable, { type Column } from '../components/DataTable.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { get, qs } from '../lib/api'
import { clock, loadModelName, monthLabel, num, shortDay, shortDayYear, time, today } from '../lib/fmt'
import type { TrainingLoad, Weekly, WorkoutList, WorkoutSummary } from '../lib/types'
import { axisTooltip, color, dayAxis, goalLine, grid, legend, valueAxis } from '../charts/helpers'

const router = useRouter()
const { range, previous, state, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => range.value.to < today())
const weekly = useAsync((s) => get<Weekly>(`/training/weekly${qs(range.value)}`, s), [range])
// The previous period of the same length, for the compare toggle.
const prevWeekly = useAsync(
  (s) => (state.value.compare ? get<Weekly>(`/training/weekly${qs(previous.value)}`, s) : Promise.resolve(null)),
  [previous, () => state.value.compare],
)
const load = useAsync((s) => get<TrainingLoad>(`/training/load${qs(range.value)}`, s), [range])
const workouts = useAsync((s) => get<WorkoutList>(`/workouts${qs({ ...range.value, limit: 500 })}`, s), [range])

const w = computed(() => weekly.data.value)
setPageMeta(() => ({ subtitle: w.value ? `${w.value.main_workout_name} · target ${w.value.target_sessions} sessions / week` : undefined }))
const sessionsOption = computed(() => {
  const weeks = w.value?.weeks ?? []
  return {
    grid: grid({ top: 20 }),
    tooltip: axisTooltip(),
    xAxis: { type: 'category', data: weeks.map((x) => shortDay(x.week_start)), axisTick: { show: false }, axisLabel: { hideOverlap: true } },
    yAxis: valueAxis(undefined, { minInterval: 1 }),
    series: [
      {
        type: 'bar',
        name: 'Sessions',
        data: weeks.map((x) => ({
          value: x.sessions,
          itemStyle: { color: x.met ? color('--positive') : x.partial ? color('--c-context') : color('--attention'), borderRadius: [4, 4, 0, 0] },
        })),
        barMaxWidth: 28,
        markLine: goalLine(w.value?.target_sessions ?? 3, 'target'),
      },
    ],
  }
})

const monthlyOption = computed(() => ({
  grid: grid(),
  legend: legend(),
  tooltip: axisTooltip(),
  xAxis: { type: 'category', data: (w.value?.monthly ?? []).map((m) => monthLabel(m.month)) },
  yAxis: [valueAxis('sessions', { minInterval: 1 }), valueAxis('h', { splitLine: { show: false } })],
  series: [
    { type: 'bar', name: 'Sessions', data: (w.value?.monthly ?? []).map((m) => m.sessions), barMaxWidth: 28, itemStyle: { color: color('--s-blue') } },
    { type: 'line', name: 'Hours', yAxisIndex: 1, data: (w.value?.monthly ?? []).map((m) => Math.round(m.hours * 10) / 10), itemStyle: { color: color('--s-orange') } },
  ],
}))

const loadOption = computed(() => {
  const d = load.data.value?.days ?? []
  return {
    grid: grid(),
    legend: legend(),
    tooltip: axisTooltip(),
    xAxis: dayAxis(d.map((x) => x.day)),
    yAxis: [valueAxis('load'), valueAxis('form', { splitLine: { show: false } })],
    series: [
      { type: 'bar', name: 'Daily load', data: d.map((x) => x.load || null), itemStyle: { color: color('--c-context') }, barMaxWidth: 10 },
      { type: 'line', name: 'Fitness (CTL)', data: d.map((x) => x.ctl), showSymbol: false, lineStyle: { width: 2 }, itemStyle: { color: color('--s-blue') } },
      { type: 'line', name: 'Fatigue (ATL)', data: d.map((x) => x.atl), showSymbol: false, lineStyle: { width: 2 }, itemStyle: { color: color('--s-orange') } },
      { type: 'line', name: 'Form (TSB)', yAxisIndex: 1, data: d.map((x) => x.tsb), showSymbol: false, lineStyle: { width: 1.5, type: 'dashed' }, itemStyle: { color: color('--s-aqua') } },
    ],
  }
})

const zoneOption = computed(() => {
  const z = w.value?.zones_by_week ?? []
  const names = ['Below Z1', 'Zone 1', 'Zone 2', 'Zone 3', 'Zone 4', 'Zone 5']
  const tokens = ['--zone0', '--zone1', '--zone2', '--zone3', '--zone4', '--zone5']
  return {
    grid: grid(),
    legend: legend({ type: 'scroll' }),
    tooltip: axisTooltip(),
    xAxis: { type: 'category', data: z.map((x) => shortDay(x.week_start)), axisLabel: { hideOverlap: true } },
    yAxis: valueAxis('min'),
    series: names.map((n, i) => ({
      type: 'bar',
      stack: 'z',
      name: n,
      data: z.map((x) => Math.round(x.zone_s[i] / 60)),
      itemStyle: { color: color(tokens[i]) },
      barMaxWidth: 28,
    })),
  }
})

const hrOption = computed(() => {
  const s = (w.value?.sessions ?? []).filter((x) => x.avg_hr_bpm !== null)
  return {
    grid: grid(),
    tooltip: {
      trigger: 'item',
      triggerOn: 'mousemove|click',
      formatter: (p: { data: [string, number, number] }) => `${shortDay(p.data[0])}<br/>avg ${num(p.data[1])} bpm · ${num(p.data[2])} min`,
    },
    xAxis: dayAxis(s.map((x) => x.day)),
    yAxis: valueAxis('bpm', { scale: true }),
    series: [
      {
        type: 'line',
        name: 'Avg session HR',
        data: s.map((x) => [x.day, x.avg_hr_bpm, Math.round((x.duration_s ?? 0) / 60)]),
        lineStyle: { width: 1.5, color: color('--c-hr') },
        itemStyle: { color: color('--c-hr') },
        symbolSize: 6,
      },
    ],
  }
})

// Headline cards: weekly consistency and volume, session intensity and fitness trend.
/** Averages over the complete weeks of a period. */
function weekAverages(wk: Weekly | null | undefined) {
  const full = (wk?.weeks ?? []).filter((x) => !x.partial)
  if (!full.length) return { sessions: null, hours: null }
  return {
    sessions: full.reduce((a, b) => a + b.sessions, 0) / full.length,
    hours: full.reduce((a, b) => a + b.hours, 0) / full.length,
  }
}
function change(cur: number | null | undefined, prev: number | null | undefined) {
  if (!state.value.compare || !prevWeekly.data.value) return undefined
  return cur != null && prev ? ((cur - prev) / prev) * 100 : null
}
const was = (v: number | null | undefined, digits: number, unit = '') =>
  state.value.compare && prevWeekly.data.value && v != null ? `${num(v, digits)}${unit ? ` ${unit}` : ''}` : undefined
const kpis = computed(() => {
  const wk = w.value
  if (!wk) return []
  const cur = weekAverages(wk)
  const pw = prevWeekly.data.value
  const prev = weekAverages(pw)
  const hrSessions = wk.sessions.filter((x) => x.avg_hr_bpm !== null)
  const ld = load.data.value
  return [
    { label: 'Sessions / week', value: num(cur.sessions, 1), delta: change(cur.sessions, prev.sessions), previous: was(prev.sessions, 1),
      series: wk.weeks.map((x) => x.sessions), seriesLabel: 'sessions per week',
      color: color('--c-exercise'), goal: { current: cur.sessions, target: wk.target_sessions },
      footnote: `${wk.totals.sessions} sessions in this period` },
    { label: 'Training time / week', value: num(cur.hours, 1), unit: 'h', delta: change(cur.hours, prev.hours), previous: was(prev.hours, 1, 'h'),
      series: wk.weeks.map((x) => x.hours), seriesLabel: 'hours per week',
      color: color('--s-orange'), footnote: `${num(wk.totals.hours, 1)} h in this period` },
    { label: 'Avg session HR', value: num(wk.totals.avg_hr_bpm), unit: 'bpm', delta: change(wk.totals.avg_hr_bpm, pw?.totals.avg_hr_bpm),
      previous: was(pw?.totals.avg_hr_bpm, 0, 'bpm'), series: hrSessions.map((x) => x.avg_hr_bpm),
      seriesLabel: 'average heart rate per session', color: color('--c-hr'), footnote: `avg session ${num(wk.totals.avg_duration_min)} min` },
    { label: 'Fitness (CTL)', value: num(ld?.latest?.ctl), series: ld?.days.map((x) => x.ctl), seriesLabel: '42-day training load',
      color: color('--s-blue'), footnote: ld?.latest ? `fatigue (ATL) ${num(ld.latest.atl)}` : undefined },
  ]
})
const strip = computed<StripItem[]>(() => {
  const wk = w.value
  const latest = load.data.value?.latest
  if (!wk) return []
  return [
    { label: 'Current streak', value: `${wk.current_streak_weeks}`, unit: 'weeks', hint: `weeks with ≥ ${wk.target_sessions} sessions` },
    { label: 'Longest streak', value: `${wk.longest_streak_weeks}`, unit: 'weeks' },
    { label: 'Form (TSB)', value: num(latest?.tsb), hint: 'fitness − fatigue; > 0 = fresh' },
    { label: 'Load ratio (ACWR)', value: num(latest?.acwr, 2), hint: '0.8–1.3 is a balanced range' },
  ]
})

const loadLabel = computed(() => (load.data.value?.load_model === 'srpe' ? 'sRPE' : 'TRIMP'))
const loadSubtitle = computed(() => {
  const l = load.data.value
  const parts = [`${loadModelName[l?.load_model ?? 'edwards']} model`, 'fitness 42-day, fatigue 7-day EWMA']
  const sc = l?.srpe_scale
  if (sc) {
    parts.push(sc.rated >= 5 ? `sessions without RPE count as TRIMP × ${num(sc.factor, 2)} (from ${sc.rated} rated)` : `rate ≥ 5 sessions to calibrate unrated ones (${sc.rated} so far)`)
  }
  return parts.join(' · ')
})
// The year is only shown when the listed workouts span more than one.
const multiYear = computed(() => new Set((workouts.data.value?.items ?? []).map((r) => r.day.slice(0, 4))).size > 1)
const columns = computed<Column<WorkoutSummary>[]>(() => [
  { key: 'day', label: 'Date', value: (r) => `${(multiYear.value ? shortDayYear : shortDay)(r.day)} ${time(r.start_ms)}`, primary: true },
  { key: 'type', label: 'Type', value: (r) => r.name, primary: true },
  { key: 'dur', label: 'Duration', value: (r) => clock(r.duration_s), align: 'right' },
  { key: 'hr', label: 'Avg HR', value: (r) => num(r.avg_hr_bpm), align: 'right' },
  { key: 'max', label: 'Max HR', value: (r) => num(r.max_hr_bpm), align: 'right' },
  { key: 'z3', label: '% Z3+', value: (r) => num(r.pct_z3_plus), align: 'right' },
  { key: 'kcal', label: 'kcal', value: (r) => num(r.energy_kcal), align: 'right' },
  { key: 'load', label: loadLabel.value, value: (r) => num(r.load), align: 'right' },
  { key: 'rpe', label: 'RPE', value: (r) => (r.rpe ? String(r.rpe) : '–'), align: 'right' },
])
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <StateBlock :loading="weekly.loading.value" :error="weekly.error.value" :empty="!w" :height="320">
      <template v-if="w">
        <div class="kpis">
          <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" />
        </div>
        <MetricStrip :items="strip" />

        <div class="cols-2">
          <Card title="Sessions per week" subtitle="Green: target met · orange: missed · grey: week in progress">
            <Chart :option="sessionsOption" :height="230" label="Gym sessions per ISO week versus target" />
          </Card>
          <Card title="Monthly volume">
            <Chart :option="monthlyOption" :height="230" label="Sessions and hours per month" />
          </Card>
          <Card title="Training load" :subtitle="loadSubtitle">
            <StateBlock :loading="load.loading.value" :error="load.error.value" :empty="!load.data.value?.days.length">
              <Chart :option="loadOption" :height="250" label="Daily training load with fitness, fatigue and form" />
            </StateBlock>
          </Card>
          <Card title="Heart-rate zones per week" subtitle="Minutes in each zone during sessions">
            <Chart :option="zoneOption" :height="250" label="Minutes per heart rate zone per week" />
          </Card>
        </div>

        <Card title="Average session heart rate">
          <Chart :option="hrOption" :height="220" label="Average heart rate of each gym session"
            @click="(p: any) => { const s = w?.sessions.filter((x) => x.avg_hr_bpm !== null)[p?.dataIndex]; if (s) router.push(`/workouts/${s.id}`) }" />
        </Card>

        <Card title="Workouts" :subtitle="workouts.data.value?.truncated ? 'Showing the latest 500' : undefined">
          <StateBlock :loading="workouts.loading.value" :error="workouts.error.value" :empty="false">
            <DataTable :rows="workouts.data.value?.items ?? []" :columns="columns" :row-key="(r) => r.id"
              empty="No workouts in this period." @select="(r) => router.push(`/workouts/${r.id}`)" />
          </StateBlock>
        </Card>
      </template>
    </StateBlock>
  </div>
</template>
