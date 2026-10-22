<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import Icon from '../components/Icon.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { useSettings } from '../composables/useSettings'
import { useSwipe } from '../composables/useSwipe'
import { get } from '../lib/api'
import { addDays, clock, hm, km, longDay, num, time, today } from '../lib/fmt'
import type { DayDetail } from '../lib/types'
import { color, isPhone } from '../charts/helpers'

const route = useRoute()
const router = useRouter()
const day = computed(() => (typeof route.params.day === 'string' && route.params.day ? route.params.day : today()))
const res = useAsync((s) => get<DayDetail>(`/days/${day.value}?bucket_s=300`, s), [day])
const d = computed(() => res.data.value)
setPageMeta(() => ({ title: longDay(day.value), subtitle: 'Swipe or use the arrows to change day' }))
const settings = useSettings()
settings.load()

// Single day: sparklines show the day's own shape (steps per hour, heart rate through the day).
const kpis = computed(() => {
  const x = d.value
  if (!x) return []
  const g = settings.data.value?.resolved.goals
  const hourly = x.activity?.steps_by_hour
  return [
    { label: 'Steps', value: num(x.activity?.steps), series: hourly, seriesLabel: 'steps per hour', color: color('--c-steps'),
      goal: g ? { current: x.activity?.steps ?? null, target: g.steps_per_day } : undefined },
    { label: 'Heart rate', value: num(x.heart?.hr_avg_bpm), unit: 'bpm avg', series: x.hr.length ? x.hr.map((p) => p.avg_bpm) : undefined,
      seriesLabel: 'heart rate through the day', color: color('--c-hr'),
      footnote: x.heart ? `range ${num(x.heart.hr_min_bpm)}–${num(x.heart.hr_max_bpm)} bpm` : undefined },
    { label: 'Exercise', value: num(x.activity?.exercise_min), unit: 'min', color: color('--c-exercise'),
      goal: g ? { current: x.activity?.exercise_min ?? null, target: g.exercise_min, unit: 'min' } : undefined,
      footnote: x.workouts.length ? `${x.workouts.length} workout${x.workouts.length > 1 ? 's' : ''}` : 'no workouts' },
    { label: 'Stand hours', value: `${x.stood_hours.length}`, unit: 'h', color: color('--c-stand'),
      goal: g ? { current: x.stood_hours.length, target: g.stand_hours, unit: 'h' } : undefined,
      footnote: x.activity?.longest_sedentary_h != null ? `longest sitting ${num(x.activity.longest_sedentary_h, 0)} h` : undefined },
  ]
})

const strip = computed<StripItem[]>(() => {
  const x = d.value
  if (!x) return []
  const items: StripItem[] = [
    { label: 'Distance', value: km(x.activity?.distance_m), unit: 'km' },
    { label: 'Active energy', value: num(x.activity?.active_kcal), unit: 'kcal',
      hint: x.activity?.basal_kcal != null ? `+ ${num(x.activity.basal_kcal)} resting` : undefined },
    { label: 'Resting HR', value: num(x.heart?.resting_hr_bpm), unit: 'bpm', hint: x.heart?.hrv_sdnn_ms != null ? `HRV ${num(x.heart.hrv_sdnn_ms)} ms` : undefined },
  ]
  items.push(x.sleep
    ? { label: 'Sleep', value: hm(x.sleep.asleep_min), hint: x.sleep.deep_min != null ? `deep ${hm(x.sleep.deep_min)}` : undefined }
    : { label: 'Floors climbed', value: num(x.activity?.flights) })
  return items
})

function go(n: number) {
  const next = addDays(day.value, n)
  if (next <= today()) router.replace(`/day/${next}`)
}
const { onTouchStart, onTouchEnd } = useSwipe(
  () => go(1),
  () => go(-1),
)

const stageColor: Record<string, string> = {
  deep: '--c-deep',
  rem: '--c-rem',
  core: '--c-core',
  asleep: '--c-sleep',
  awake: '--c-awake',
}

// Three stacked panels sharing one time axis (no dual y-axes): heart rate, steps per hour and a
// stood strip. Each panel keeps its own gridlines, so hiding one never leaves another without them.
const PANEL = { left: 52, right: 16 }
const option = computed(() => {
  const x = d.value
  if (!x) return {}
  const hourMs = 3600000
  // x values are milliseconds since the day's start, labelled in the server's zone, so the axis
  // does not depend on the browser's time zone.
  const rel = (ms: number) => ms - x.start_ms
  const span = x.end_ms - x.start_ms
  const steps = (x.activity?.steps_by_hour ?? []).map((v, h) => [h * hourMs + hourMs / 2, v])
  const hr = x.hr.map((p) => [rel(p.t_ms), p.avg_bpm])
  const stood = x.stood_hours.map((h) => [h * hourMs + hourMs / 2, 1])
  const fill = (token: string, alpha: number) => {
    const hex = color(token)
    const a = Math.round(alpha * 255).toString(16).padStart(2, '0')
    return hex.startsWith('#') && hex.length === 7 ? hex + a : hex
  }
  const workoutAreas = (labelled: boolean) =>
    x.workouts.map((w) => [
      {
        xAxis: rel(w.start_ms),
        name: labelled ? w.name : '',
        itemStyle: { color: fill('--c-active', 0.18) },
        label: labelled
          ? {
              show: true,
              position: 'insideTop',
              color: color('--text'),
              fontSize: 11,
              fontWeight: 600,
              backgroundColor: color('--elevated'),
              borderColor: color('--c-active'),
              borderWidth: 1,
              borderRadius: 4,
              padding: [2, 6],
              distance: 4,
            }
          : { show: false },
      },
      { xAxis: rel(w.end_ms) },
    ])
  const sleepAreas = x.sleep_segments
    .filter((s) => s.stage !== 'in_bed' && s.end_ms > x.start_ms)
    .map((s) => [
      { xAxis: rel(Math.max(s.start_ms, x.start_ms)), itemStyle: { color: fill(stageColor[s.stage] ?? '--c-sleep', 0.2) }, label: { show: false } },
      { xAxis: rel(Math.min(s.end_ms, x.end_ms)) },
    ])
  const interval = (isPhone() ? 6 : 3) * hourMs
  const timeAxis = (gridIndex: number, labels: boolean) => ({
    type: 'value',
    gridIndex,
    min: 0,
    max: span,
    interval,
    splitLine: { show: true, lineStyle: { color: color('--border'), opacity: 0.6 } },
    axisLabel: labels ? { formatter: (v: number) => time(x.start_ms + v), hideOverlap: true } : { show: false },
    axisTick: { show: labels },
  })
  return {
    grid: [
      { ...PANEL, top: 28, height: 150 },
      { ...PANEL, top: 212, height: 80 },
      { ...PANEL, top: 306, height: 14 },
    ],
    axisPointer: { link: [{ xAxisIndex: 'all' }] },
    tooltip: {
      trigger: 'axis',
      triggerOn: 'mousemove|click',
      axisPointer: { type: 'line' },
      formatter: (ps: { axisValue: number; marker: string; seriesName: string; value: [number, number] }[]) =>
        [time(x.start_ms + ps[0].axisValue), ...ps.map((p) => `${p.marker}${p.seriesName} <b>${num(p.value[1])}</b>`)].join('<br/>'),
    },
    xAxis: [timeAxis(0, false), timeAxis(1, false), timeAxis(2, true)],
    yAxis: [
      { type: 'value', gridIndex: 0, name: 'bpm', nameGap: 10, scale: true, splitNumber: 3 },
      { type: 'value', gridIndex: 1, name: 'steps / h', nameGap: 10, splitNumber: 2 },
      {
        type: 'value',
        gridIndex: 2,
        max: 1,
        axisLabel: { show: false },
        splitLine: { show: false },
        name: 'stood',
        nameLocation: 'middle',
        nameRotate: 0,
        nameGap: 8,
        nameTextStyle: { align: 'right', color: color('--muted'), fontSize: 11 },
      },
    ],
    series: [
      {
        type: 'line',
        name: 'Heart rate',
        xAxisIndex: 0,
        yAxisIndex: 0,
        data: hr,
        showSymbol: false,
        lineStyle: { width: 1.5, color: color('--c-hr') },
        itemStyle: { color: color('--c-hr') },
        markArea: { silent: true, data: [...workoutAreas(true), ...sleepAreas] },
      },
      {
        type: 'bar',
        name: 'Steps / hour',
        xAxisIndex: 1,
        yAxisIndex: 1,
        data: steps,
        barWidth: '60%',
        itemStyle: { color: color('--c-steps'), borderRadius: [3, 3, 0, 0] },
        markArea: { silent: true, data: [...workoutAreas(false), ...sleepAreas] },
      },
      {
        type: 'bar',
        name: 'Stood',
        xAxisIndex: 2,
        yAxisIndex: 2,
        data: stood,
        barWidth: '70%',
        itemStyle: { color: color('--c-context'), borderRadius: 3 },
        tooltip: { show: false },
      },
    ],
  }
})
</script>

<template>
  <div class="page" @touchstart.passive="onTouchStart" @touchend.passive="onTouchEnd">
    <Teleport defer to="#topbar-actions">
      <div class="daynav">
        <button class="arrow" type="button" aria-label="Previous day" @click="go(-1)"><Icon name="left" :size="18" /></button>
        <input class="dateinput" type="date" :value="day" :max="today()" aria-label="Pick a day"
          @change="(e) => router.replace(`/day/${(e.target as HTMLInputElement).value}`)" />
        <button class="arrow" type="button" aria-label="Next day" :disabled="day >= today()" @click="go(1)"><Icon name="right" :size="18" /></button>
      </div>
      <button class="btn small" type="button" :disabled="day === today()" @click="router.replace(`/day/${today()}`)">Today</button>
    </Teleport>

    <StateBlock :loading="res.loading.value" :error="res.error.value" :empty="!d" :height="320">
      <template v-if="d">
        <div class="kpis">
          <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" />
        </div>
        <MetricStrip :items="strip" />
        <p v-if="d.activity?.summary_mismatch" class="chip attention mismatch" role="status">
          <Icon name="alert" :size="13" /> Active energy differs from the Watch's daily summary
          ({{ num(d.activity.active_kcal) }} vs {{ num(d.activity.rings.move_kcal) }} kcal) by more than 10 %
        </p>

        <Card title="24-hour timeline" subtitle="Heart rate (5-min average), steps per hour and hours stood · shaded: workouts (orange) and sleep stages">
          <StateBlock :empty="!d.hr.length && !d.activity" empty-text="No data recorded on this day.">
            <Chart :option="option" :height="352" label="24-hour timeline of steps, heart rate, stand hours, workouts and sleep" />
          </StateBlock>
        </Card>

        <div class="cols-2">
          <Card title="Workouts">
            <StateBlock :empty="!d.workouts.length" empty-text="No workouts on this day.">
              <ul class="list">
                <li v-for="w in d.workouts" :key="w.id">
                  <RouterLink :to="`/workouts/${w.id}`">
                    <strong>{{ w.name }}</strong>
                    <span class="text-2 small tnum">{{ time(w.start_ms) }} · {{ clock(w.duration_s) }} · avg {{ num(w.avg_hr_bpm) }} bpm ·
                      {{ num(w.energy_kcal) }} kcal</span>
                  </RouterLink>
                </li>
              </ul>
            </StateBlock>
          </Card>
          <Card v-if="d.sleep" title="Last night">
            <div class="kv">
              <span>Bedtime</span><strong class="tnum">{{ d.sleep.bedtime_ms ? time(d.sleep.bedtime_ms) : '–' }}</strong>
              <span>Wake</span><strong class="tnum">{{ d.sleep.wake_ms ? time(d.sleep.wake_ms) : '–' }}</strong>
              <span>Asleep</span><strong>{{ hm(d.sleep.asleep_min) }}</strong>
              <span>Deep / REM / Core</span><strong class="tnum">{{ hm(d.sleep.deep_min) }} / {{ hm(d.sleep.rem_min) }} / {{ hm(d.sleep.core_min) }}</strong>
              <span>Sleeping HR</span><strong class="tnum">{{ num(d.heart?.hr_sleep_avg_bpm) }} bpm</strong>
            </div>
          </Card>
        </div>
      </template>
    </StateBlock>
  </div>
</template>

<style scoped>
.daynav {
  display: inline-flex;
  align-items: center;
  height: 44px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.arrow {
  display: grid;
  place-items: center;
  width: 40px;
  height: 100%;
  border: 0;
  background: transparent;
  color: var(--text-2);
  cursor: pointer;
}
.arrow:hover:not(:disabled) {
  color: var(--text);
}
.arrow:disabled {
  opacity: 0.35;
  cursor: default;
}
.dateinput {
  height: 100%;
  border: 0;
  background: transparent;
  color: var(--text);
  font-weight: 600;
  font-size: 0.875rem;
  padding: 0 4px;
  color-scheme: dark;
}
.dateinput:focus {
  outline: none;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.list a {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--elevated);
  color: var(--text);
}
.list a:hover {
  text-decoration: none;
  outline: 1px solid var(--border-strong);
}
.kv {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px 16px;
  font-size: 0.92rem;
}
.kv span {
  color: var(--text-2);
}
.mismatch {
  align-self: flex-start;
  white-space: normal;
}
</style>
