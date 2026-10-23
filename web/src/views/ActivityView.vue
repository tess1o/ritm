<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import ActivityRings from '../components/ActivityRings.vue'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { useDayAxis, openDayHandler } from '../composables/useDayAxis'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { useSettings } from '../composables/useSettings'
import { get, qs } from '../lib/api'
import { addDays, km, mean, num, parseDay, rolling, today } from '../lib/fmt'
import type { DailyActivity, RangeItems } from '../lib/types'
import { axisTooltip, color, dayAxis, goalLine, grid, isPhone, legend, valueAxis } from '../charts/helpers'

const router = useRouter()
const { range, previous, state, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => range.value.to < today())
const settings = useSettings()
settings.load()

// Load 6 extra days before the period for the 7-day rolling average.
const res = useAsync(
  (s) => get<RangeItems<DailyActivity>>(`/activity/daily${qs({ from: addDays(range.value.from, -6), to: range.value.to })}`, s),
  [range],
)
const prev = useAsync(
  (s) =>
    state.value.compare
      ? get<RangeItems<DailyActivity>>(`/activity/daily${qs({ ...previous.value, hours: false })}`, s)
      : Promise.resolve(null),
  [previous, () => state.value.compare],
)

const allDays = computed(() => res.data.value?.items ?? [])
const days = computed(() => allDays.value.filter((d) => d.day >= range.value.from))
const watchDays = computed(() => days.value.filter((d) => d.has_watch_data))
const goal = computed(() => settings.data.value?.resolved.goals.steps_per_day ?? 8000)

/** Fill missing days so the x-axis is continuous. */
const axisDays = useDayAxis(range)
const byDay = computed(() => new Map(allDays.value.map((d) => [d.day, d])))
const series = (f: (d: DailyActivity) => number | null) =>
  axisDays.value.map((d) => {
    const x = byDay.value.get(d)
    return x && x.has_watch_data ? f(x) : null
  })

function avg(list: DailyActivity[], f: (d: DailyActivity) => number | null) {
  return mean(list.filter((d) => d.has_watch_data).map(f))
}
function delta(f: (d: DailyActivity) => number | null) {
  const p = prev.data.value?.items
  if (!state.value.compare || !p) return undefined
  const a = avg(days.value, f)
  const b = avg(p, f)
  return a !== null && b ? ((a - b) / b) * 100 : null
}

const goals = computed(() => settings.data.value?.resolved.goals)
const avgOf = (f: (d: DailyActivity) => number | null) => avg(days.value, f)
const prevOf = (f: (d: DailyActivity) => number | null, digits = 0, unit = '') => {
  const p = prev.data.value?.items
  if (!state.value.compare || !p) return undefined
  const v = avg(p, f)
  return v === null ? undefined : `${num(v, digits)}${unit ? ` ${unit}` : ''}`
}

const f = {
  steps: (d: DailyActivity) => d.steps,
  active: (d: DailyActivity) => d.active_kcal,
  exercise: (d: DailyActivity) => d.exercise_min,
  stand: (d: DailyActivity) => d.stand_hours,
  distance: (d: DailyActivity) => d.distance_m,
  floors: (d: DailyActivity) => d.flights,
  sitting: (d: DailyActivity) => d.longest_sedentary_h,
}

// Headline metrics: value, change, previous value, daily sparkline and goal progress.
const kpis = computed(() => [
  {
    label: 'Steps / day',
    value: num(avgOf(f.steps)),
    delta: delta(f.steps),
    previous: prevOf(f.steps),
    series: series(f.steps),
    color: color('--c-steps'),
    goal: { current: avgOf(f.steps), target: goal.value },
  },
  {
    label: 'Active energy / day',
    value: num(avgOf(f.active)),
    unit: 'kcal',
    delta: delta(f.active),
    previous: prevOf(f.active, 0, 'kcal'),
    series: series(f.active),
    color: color('--c-active'),
  },
  {
    label: 'Exercise / day',
    value: num(avgOf(f.exercise)),
    unit: 'min',
    delta: delta(f.exercise),
    previous: prevOf(f.exercise, 0, 'min'),
    series: series(f.exercise),
    color: color('--c-exercise'),
    goal: goals.value ? { current: avgOf(f.exercise), target: goals.value.exercise_min, unit: 'min' } : undefined,
  },
  {
    label: 'Stand hours / day',
    value: num(avgOf(f.stand), 1),
    unit: 'h',
    delta: delta(f.stand),
    previous: prevOf(f.stand, 1, 'h'),
    series: series(f.stand),
    color: color('--c-stand'),
    goal: goals.value ? { current: avgOf(f.stand), target: goals.value.stand_hours, unit: 'h' } : undefined,
  },
])

// Secondary metrics in one compact strip.
const strip = computed(() => {
  const hit = watchDays.value.filter((d) => (d.steps ?? 0) >= goal.value).length
  return [
    { label: 'Distance / day', value: km(avgOf(f.distance)), unit: 'km', delta: delta(f.distance) },
    { label: 'Floors climbed / day', value: num(avgOf(f.floors), 1), delta: delta(f.floors), hint: '1 floor ≈ 3 m of climb' },
    { label: 'Longest sitting stretch', value: num(avgOf(f.sitting), 1), unit: 'h', delta: delta(f.sitting), lower: true, hint: 'waking hours, average per day' },
    { label: 'Step goal reached', value: `${hit}`, unit: `of ${watchDays.value.length} days`, hint: `goal ${num(goal.value)} steps` },
  ]
})

const stepsOption = computed(() => {
  const all = axisDays.value.map((d) => byDay.value.get(d))
  const ext: (number | null)[] = []
  for (let d = addDays(range.value.from, -6); d <= range.value.to; d = addDays(d, 1)) {
    const x = byDay.value.get(d)
    ext.push(x?.has_watch_data ? x.steps : null)
  }
  const avg7 = rolling(ext, 7).slice(6)
  return {
    grid: grid(),
    legend: legend(),
    tooltip: axisTooltip(),
    xAxis: dayAxis(axisDays.value),
    yAxis: valueAxis(),
    series: [
      {
        type: 'bar',
        name: 'Steps',
        data: all.map((x) => ({
          value: x?.has_watch_data ? x.steps : null,
          itemStyle: { color: (x?.steps ?? 0) >= goal.value ? color('--positive') : color('--c-steps') },
        })),
        barMaxWidth: 22,
        itemStyle: { borderRadius: [3, 3, 0, 0] },
        markLine: goalLine(goal.value, `goal ${num(goal.value)}`),
      },
      { type: 'line', name: '7-day average', data: avg7, showSymbol: false, smooth: true, lineStyle: { width: 2, color: color('--text-2') }, itemStyle: { color: color('--text-2') } },
    ],
  }
})

const distanceOption = computed(() => ({
  grid: grid(),
  tooltip: axisTooltip(),
  xAxis: dayAxis(axisDays.value),
  yAxis: valueAxis('km'),
  series: [
    {
      type: 'line',
      name: 'Distance (km)',
      data: series((d) => (d.distance_m === null ? null : Math.round(d.distance_m / 10) / 100)),
      showSymbol: axisDays.value.length <= 31,
      areaStyle: { opacity: 0.12 },
      lineStyle: { width: 2 },
      connectNulls: false,
    },
  ],
}))

const energyOption = computed(() => ({
  grid: grid(),
  legend: legend(),
  tooltip: axisTooltip(),
  xAxis: dayAxis(axisDays.value),
  yAxis: valueAxis('kcal'),
  series: [
    { type: 'bar', stack: 'e', name: 'Active', data: series((d) => d.active_kcal), itemStyle: { color: color('--c-active') }, barMaxWidth: 22 },
    { type: 'bar', stack: 'e', name: 'Resting', data: series((d) => d.basal_kcal), itemStyle: { color: color('--c-basal') }, barMaxWidth: 22 },
  ],
}))

const standOption = computed(() => ({
  grid: grid(),
  legend: legend(),
  tooltip: axisTooltip(),
  xAxis: dayAxis(axisDays.value),
  yAxis: [valueAxis('h'), valueAxis('min', { splitLine: { show: false } })],
  series: [
    { type: 'bar', name: 'Stand hours', data: series((d) => d.stand_hours), itemStyle: { color: color('--c-stand') }, barMaxWidth: 16 },
    { type: 'bar', name: 'Exercise min', yAxisIndex: 1, data: series((d) => d.exercise_min), itemStyle: { color: color('--c-exercise') }, barMaxWidth: 16 },
  ],
}))

// hour-of-day × weekday heatmap of mean steps
const heatOption = computed(() => {
  const sums = Array.from({ length: 7 }, () => Array(24).fill(0))
  const counts = Array(7).fill(0)
  for (const d of watchDays.value) {
    if (!d.steps_by_hour) continue
    const wd = (parseDay(d.day).getDay() + 6) % 7
    counts[wd]++
    d.steps_by_hour.forEach((v, h) => (sums[wd][h] += v))
  }
  const data: [number, number, number][] = []
  let maxV = 0
  for (let wd = 0; wd < 7; wd++)
    for (let h = 0; h < 24; h++) {
      const v = counts[wd] ? Math.round(sums[wd][h] / counts[wd]) : 0
      maxV = Math.max(maxV, v)
      data.push([h, wd, v])
    }
  const phone = isPhone()
  return {
    grid: grid({ top: 8, bottom: 30 }),
    tooltip: {
      triggerOn: 'mousemove|click',
      formatter: (p: { value: [number, number, number] }) =>
        `${['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'][p.value[1]]} ${String(p.value[0]).padStart(2, '0')}:00<br/><b>${num(p.value[2])}</b> steps (avg)`,
    },
    xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, h) => String(h)), axisLabel: { interval: phone ? 5 : 2 }, splitArea: { show: false } },
    yAxis: { type: 'category', data: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'], inverse: true },
    visualMap: {
      min: 0,
      max: Math.max(maxV, 1),
      calculable: false,
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      itemWidth: 10,
      itemHeight: 120,
      textStyle: { color: color('--muted'), fontSize: 10 },
      inRange: { color: [color('--surface'), color('--s-blue-dark'), color('--s-blue'), color('--s-blue-light')] },
    },
    series: [{ type: 'heatmap', data, itemStyle: { borderColor: color('--bg'), borderWidth: 1 } }],
  }
})

// Paired comparisons of average steps per day, each with a plain-language takeaway.
const pairs = computed(() => {
  const w = watchDays.value
  const isWeekend = (d: string) => [0, 6].includes(parseDay(d).getDay())
  const group = (label: string, list: DailyActivity[]) => ({ label, value: avg(list, f.steps), n: list.length })
  const build = (title: string, a: ReturnType<typeof group>, b: ReturnType<typeof group>, more: string, less: string) => {
    let takeaway = 'Not enough days in this period to compare.'
    if (a.value !== null && b.value !== null && a.n >= 2 && b.n >= 2) {
      const r = a.value / b.value
      takeaway =
        Math.abs(r - 1) < 0.1
          ? 'About the same either way.'
          : r > 1
            ? `${num(r, 1)}× more steps ${more}.`
            : `${num(1 / r, 1)}× more steps ${less}.`
    }
    const scale = Math.max(a.value ?? 0, b.value ?? 0, goal.value) * 1.08
    return { title, takeaway, items: [a, b], scale }
  }
  return [
    build('Workdays vs weekend', group('Workdays', w.filter((d) => !isWeekend(d.day))), group('Weekend', w.filter((d) => isWeekend(d.day))),
      'on workdays', 'at weekends'),
    build('Gym days vs other days', group('Gym days', w.filter((d) => d.is_gym_day)), group('Other days', w.filter((d) => !d.is_gym_day)),
      'on gym days', 'on non-gym days'),
  ]
})

const mismatchDays = computed(() => days.value.filter((d) => d.summary_mismatch).length)

const openDay = openDayHandler(router)
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <StateBlock :loading="res.loading.value" :error="res.error.value" :empty="!days.length" :height="320">
      <div class="kpis">
        <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" />
      </div>
      <MetricStrip :items="strip" vs="previous period" />

      <Card title="Steps" :subtitle="`Daily steps with goal and 7-day average · tap a bar for the day view`">
        <Chart :option="stepsOption" :height="260" label="Daily steps" @click="openDay" />
        <p v-if="mismatchDays" class="muted small">
          ⚠ {{ mismatchDays }} day(s) where active energy differs from the Watch's daily summary by more than 10 %.
        </p>
      </Card>

      <div class="cols-2">
        <Card title="Walking distance">
          <Chart :option="distanceOption" :height="220" label="Daily walking distance" @click="openDay" />
        </Card>
        <Card title="Energy" subtitle="Active and resting calories">
          <Chart :option="energyOption" :height="220" label="Active and resting calories per day" @click="openDay" />
        </Card>
        <Card title="Stand & exercise">
          <Chart :option="standOption" :height="220" label="Stand hours and exercise minutes per day" @click="openDay" />
        </Card>
        <Card title="When you move" subtitle="Average steps by hour and weekday">
          <Chart :option="heatOption" :height="240" label="Heatmap of average steps by hour of day and weekday" />
        </Card>
      </div>

      <div class="split">
        <Card title="Activity rings" subtitle="Daily progress towards your Watch ring goals · tap a day to open it">
          <ActivityRings :days="days" @select="(d) => router.push(`/day/${d}`)" />
        </Card>

        <Card title="Comparisons" subtitle="Average steps per day · dashed line = your step goal">
          <div class="pairs">
            <section v-for="p in pairs" :key="p.title" class="pair">
              <h3>{{ p.title }}</h3>
              <p class="takeaway">{{ p.takeaway }}</p>
              <div v-for="it in p.items" :key="it.label" class="bar-row">
                <span class="bar-label">{{ it.label }} <span class="muted small">· {{ it.n }} d</span></span>
                <div class="bar-track">
                  <div class="bar-fill" :style="{ width: `${((it.value ?? 0) / p.scale) * 100}%` }" />
                  <div class="bar-goal" :style="{ left: `${(goal / p.scale) * 100}%` }" />
                </div>
                <span class="bar-value tnum">{{ num(it.value) }}</span>
              </div>
            </section>
          </div>
        </Card>
      </div>
    </StateBlock>
  </div>
</template>

<style scoped>
.split {
  display: grid;
  gap: var(--gap);
  grid-template-columns: minmax(0, 1fr);
}
.pairs {
  display: flex;
  flex-direction: column;
  gap: 22px;
}
.pair {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.takeaway {
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--text);
}
.pair h3 {
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--muted);
}
.bar-row {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr) 64px;
  align-items: center;
  gap: 10px;
}
.bar-label {
  font-size: 0.875rem;
  color: var(--text-2);
  white-space: nowrap;
}
.bar-track {
  position: relative;
  height: 14px;
  border-radius: 4px;
  background: var(--elevated);
}
.bar-fill {
  height: 100%;
  border-radius: 4px;
  background: var(--c-steps);
}
.bar-goal {
  position: absolute;
  top: -4px;
  bottom: -4px;
  border-left: 2px dashed var(--muted);
}
.bar-value {
  text-align: right;
  font-weight: 600;
}
@media (min-width: 1024px) {
  .split {
    grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
    align-items: start;
  }
}
</style>
