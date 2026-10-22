<script setup lang="ts">
import { computed } from 'vue'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { useDayAxis } from '../composables/useDayAxis'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { get, qs } from '../lib/api'
import { hm, mean, minutesOfDay, num, time, today } from '../lib/fmt'
import type { RangeItems, RecoveryReport, SleepNight } from '../lib/types'
import { axisTooltip, color, dayAxis, grid, legend, valueAxis } from '../charts/helpers'

const { range, previous, state, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => range.value.to < today())
const sleep = useAsync((s) => get<RangeItems<SleepNight>>(`/sleep/nights${qs(range.value)}`, s), [range])
const rec = useAsync((s) => get<RecoveryReport>(`/sleep/recovery${qs(range.value)}`, s), [range])

const prevSleep = useAsync(
  (s) => (state.value.compare ? get<RangeItems<SleepNight>>(`/sleep/nights${qs(previous.value)}`, s) : Promise.resolve(null)),
  [previous, () => state.value.compare],
)
const nights = computed(() => sleep.data.value?.items ?? [])
const axisDays = useDayAxis(range)
const byDay = computed(() => new Map(nights.value.map((n) => [n.day, n])))
const hasStages = computed(() => nights.value.some((n) => n.deep_min !== null))
const h = (v: number | null | undefined) => (v === null || v === undefined ? null : Math.round((v / 60) * 100) / 100)

const stagesOption = computed(() => {
  const get = (f: (n: SleepNight) => number | null) => axisDays.value.map((d) => h(byDay.value.get(d) ? f(byDay.value.get(d)!) : null))
  const series = hasStages.value
    ? [
        { name: 'Deep', f: (n: SleepNight) => n.deep_min, c: '--c-deep' },
        { name: 'REM', f: (n: SleepNight) => n.rem_min, c: '--c-rem' },
        { name: 'Core', f: (n: SleepNight) => n.core_min, c: '--c-core' },
        { name: 'Awake', f: (n: SleepNight) => n.awake_min, c: '--c-awake' },
      ]
    : [{ name: 'Asleep', f: (n: SleepNight) => n.asleep_min, c: '--c-sleep' }]
  return {
    grid: grid(),
    legend: legend(),
    tooltip: axisTooltip(),
    xAxis: dayAxis(axisDays.value),
    yAxis: valueAxis('h'),
    series: series.map((s) => ({ type: 'bar', stack: 's', name: s.name, data: get(s.f), itemStyle: { color: color(s.c) }, barMaxWidth: 22 })),
  }
})

// Next-day recovery after training vs rest days (UC6), computed by the server with the same rule
// as the recovery insight.
const recovery = computed(() => rec.data.value)

const avgBed = computed(() => {
  const mins = nights.value
    .filter((n) => n.bedtime_ms)
    .map((n) => {
      let m = minutesOfDay(n.bedtime_ms!)
      if (m < 12 * 60) m += 24 * 60
      return m
    })
  const m = mean(mins)
  if (m === null) return '–'
  const t = Math.round(m) % (24 * 60)
  return `${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`
})
const nightly = (f: (n: SleepNight) => number | null) =>
  axisDays.value.map((d) => {
    const n = byDay.value.get(d)
    const v = n ? f(n) : null
    return v === null ? null : Math.round((v / 60) * 100) / 100
  })
const avgMin = (f: (n: SleepNight) => number | null) => mean(nights.value.map(f))
const efficiency = computed(() => {
  const a = avgMin((n) => n.asleep_min)
  const b = avgMin((n) => n.in_bed_min)
  return a !== null && b ? (a / b) * 100 : null
})

type Field = (n: SleepNight) => number | null
/** Delta and previous value vs the previous period, when the compare toggle is on. */
function vsPrev(f: Field) {
  const p = prevSleep.data.value?.items
  if (!state.value.compare || !p) return {}
  const b = mean(p.map(f))
  const a = avgMin(f)
  return { delta: a !== null && b ? ((a - b) / b) * 100 : null, previous: b === null ? undefined : hm(b) }
}
const kpis = computed(() => {
  const asleep = { label: 'Asleep / night', value: hm(avgMin((n) => n.asleep_min)), series: nightly((n) => n.asleep_min),
    seriesLabel: 'hours asleep per night', color: color('--c-sleep'), footnote: `${nights.value.length} nights recorded`, ...vsPrev((n) => n.asleep_min) }
  if (!hasStages.value) {
    return [
      asleep,
      { label: 'In bed / night', value: hm(avgMin((n) => n.in_bed_min)), series: nightly((n) => n.in_bed_min), color: color('--s-blue'),
        ...vsPrev((n) => n.in_bed_min) },
    ]
  }
  const share = (f: Field) => {
    const v = avgMin(f)
    const a = avgMin((n) => n.asleep_min)
    return v !== null && a ? `${num((v / a) * 100)} % of sleep` : undefined
  }
  const stage = (label: string, f: Field, token: string) => ({
    label, value: hm(avgMin(f)), series: nightly(f), color: color(token), footnote: share(f), ...vsPrev(f),
  })
  return [
    asleep,
    stage('Deep / night', (n) => n.deep_min, '--c-deep'),
    stage('REM / night', (n) => n.rem_min, '--c-rem'),
    stage('Core / night', (n) => n.core_min, '--c-core'),
  ]
})
const strip = computed<StripItem[]>(() => [
  { label: 'Average bedtime', value: avgBed.value },
  { label: 'Last wake-up', value: lastWake.value },
  { label: 'In bed / night', value: hm(avgMin((n) => n.in_bed_min)) },
  { label: 'Sleep efficiency', value: num(efficiency.value), unit: '%', hint: 'time asleep while in bed' },
])

const lastWake = computed(() => {
  const n = [...nights.value].reverse().find((x) => x.wake_ms)
  return n?.wake_ms ? time(n.wake_ms) : '–'
})
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <StateBlock :loading="sleep.loading.value" :error="sleep.error.value" :empty="!nights.length" :height="300"
      empty-text="No sleep data in this period — wear the Watch at night to see sleep and recovery.">
      <div class="kpis">
        <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" />
      </div>
      <MetricStrip :items="strip" />
      <Card title="Sleep per night" subtitle="Each night is shown on the day you woke up">
        <Chart :option="stagesOption" :height="260" label="Sleep duration and stages per night" />
      </Card>
      <Card title="Next-day recovery" subtitle="Morning values after a training day vs after a rest day">
        <StateBlock :empty="!recovery || recovery.after_training.days < 2 || recovery.after_rest.days < 2" empty-text="Not enough training and rest days in this period.">
          <div v-if="recovery" class="rec">
            <div />
            <strong class="small text-2">After training ({{ recovery.after_training.days }})</strong>
            <strong class="small text-2">After rest ({{ recovery.after_rest.days }})</strong>
            <span class="text-2">Resting HR</span>
            <span class="tnum big">{{ num(recovery.after_training.resting_hr_bpm, 1) }} <small class="muted">bpm</small></span>
            <span class="tnum big">{{ num(recovery.after_rest.resting_hr_bpm, 1) }} <small class="muted">bpm</small></span>
            <span class="text-2">HRV</span>
            <span class="tnum big">{{ num(recovery.after_training.hrv_sdnn_ms) }} <small class="muted">ms</small></span>
            <span class="tnum big">{{ num(recovery.after_rest.hrv_sdnn_ms) }} <small class="muted">ms</small></span>
          </div>
        </StateBlock>
      </Card>
    </StateBlock>
  </div>
</template>

<style scoped>
.rec {
  display: grid;
  grid-template-columns: auto 1fr 1fr;
  gap: 10px 16px;
  align-items: baseline;
}
.big {
  font-size: 1.3rem;
  font-weight: 600;
}
</style>
