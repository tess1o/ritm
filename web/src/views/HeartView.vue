<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { useDayAxis, openDayHandler } from '../composables/useDayAxis'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { get, qs } from '../lib/api'
import { addDays, dateTime, mean, num, rolling, today } from '../lib/fmt'
import type { DailyHeart, HeartEvent, RangeItems } from '../lib/types'
import { axisTooltip, color, dayAxis, grid, legend, tint, valueAxis } from '../charts/helpers'

type Metric = 'resting_hr_bpm' | 'hrv_sdnn_ms' | 'walking_hr_bpm'

const router = useRouter()
const { range, previous, state, length, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => range.value.to < today())
// 90 extra days give each trend a personal baseline band.
const res = useAsync((s) => get<RangeItems<DailyHeart>>(`/heart/daily${qs({ from: addDays(range.value.from, -Math.max(90, length.value)), to: range.value.to })}`, s), [range])
const events = useAsync(
  (s) => get<RangeItems<HeartEvent>>(`/heart/events${qs(range.value)}`, s),
  [range],
)
const prev = computed(() => (res.data.value?.items ?? []).filter((d) => d.day >= previous.value.from && d.day <= previous.value.to))

const all = computed(() => res.data.value?.items ?? [])
const byDay = computed(() => new Map(all.value.map((d) => [d.day, d])))
const days = computed(() => all.value.filter((d) => d.day >= range.value.from))
const axisDays = useDayAxis(range)
const col = (f: (d: DailyHeart) => number | null) => axisDays.value.map((d) => (byDay.value.get(d) ? f(byDay.value.get(d)!) : null))

function stats(m: Metric) {
  const from90 = addDays(range.value.from, -90)
  const base = all.value.filter((d) => d.day < range.value.from && d.day >= from90).map((d) => d[m]).filter((v): v is number => v !== null)
  if (base.length < 10) return null
  const sorted = [...base].sort((a, b) => a - b)
  const med = sorted[Math.floor(sorted.length / 2)]
  const sd = Math.sqrt(base.reduce((s, v) => s + (v - med) ** 2, 0) / base.length)
  return { med, sd }
}

function avgOf(list: DailyHeart[], m: keyof DailyHeart) {
  return mean(list.map((d) => d[m] as number | null))
}
function delta(m: keyof DailyHeart) {
  if (!state.value.compare) return undefined
  const a = avgOf(days.value, m)
  const b = avgOf(prev.value, m)
  return a !== null && b ? ((a - b) / b) * 100 : null
}

function trend(m: Metric, name: string, unit: string, token: string) {
  const vals = col((d) => d[m])
  // rolling mean needs prior days too
  const ext = [...all.value.filter((d) => d.day < range.value.from).slice(-6).map((d) => d[m]), ...vals]
  const roll = rolling(ext, 7).slice(ext.length - vals.length)
  const b = stats(m)
  // Days inside the usual range stay quiet; days outside it get the full colour.
  const side = (v: number | null) => (v === null || !b ? 0 : v > b.med + b.sd ? 1 : v < b.med - b.sd ? -1 : 0)
  const dense = axisDays.value.length > 120
  const dots = vals.map((v) =>
    v === null || side(v) === 0
      ? { value: v, symbolSize: dense ? 3 : 5, itemStyle: { color: tint(token, 0.5) } }
      : { value: v, symbolSize: dense ? 5 : 8, itemStyle: { color: color(token), borderColor: color('--surface'), borderWidth: 1.5 } },
  )
  const edge = { color: color(token), opacity: 0.4, width: 1, type: 'solid' }
  return {
    grid: grid(),
    legend: legend(),
    tooltip: axisTooltip((params) => {
      const ps = params as { axisValue: string; seriesIndex: number; value: number | null; marker: string; seriesName: string }[]
      const rows = ps.filter((p) => p.value !== null && p.value !== undefined).map((p) => `${p.marker}${p.seriesName} <b>${num(p.value)}</b> ${unit}`)
      const s = side(ps.find((p) => p.seriesIndex === 0)?.value ?? null)
      if (s) rows.push(`<span style="color:${color('--text-2')}">${s > 0 ? 'Above' : 'Below'} your usual range</span>`)
      return [ps[0]?.axisValue, ...rows].join('<br/>')
    }),
    xAxis: dayAxis(axisDays.value),
    yAxis: valueAxis(unit, { scale: true }),
    series: [
      {
        type: 'scatter',
        name: `${name} (daily)`,
        data: dots,
        itemStyle: { color: color(token) },
        markArea: b
          ? {
              silent: true,
              itemStyle: { color: color(token), opacity: 0.1 },
              label: { show: true, position: 'insideTopLeft', color: color('--text-2'), fontSize: 10, formatter: 'Your usual range' },
              data: [[{ yAxis: b.med - b.sd }, { yAxis: b.med + b.sd }]],
            }
          : undefined,
        markLine: b
          ? { silent: true, symbol: 'none', label: { show: false }, lineStyle: edge, data: [{ yAxis: b.med - b.sd }, { yAxis: b.med + b.sd }] }
          : undefined,
      },
      { type: 'line', name: '7-day mean', data: roll, showSymbol: false, smooth: true, connectNulls: true, lineStyle: { width: 2, color: color(token) }, itemStyle: { color: color(token) } },
    ],
  }
}

const rhrOption = computed(() => trend('resting_hr_bpm', 'Resting HR', 'bpm', '--c-hr'))
const hrvOption = computed(() => trend('hrv_sdnn_ms', 'HRV (SDNN)', 'ms', '--c-hrv'))
const walkOption = computed(() => trend('walking_hr_bpm', 'Walking HR', 'bpm', '--c-walk'))

const rangeOption = computed(() => {
  const lo = col((d) => d.hr_min_bpm)
  const hi = col((d) => d.hr_max_bpm)
  return {
    grid: grid(),
    legend: legend({ data: ['Average', 'Min–max'] }),
    tooltip: {
      trigger: 'axis',
      triggerOn: 'mousemove|click',
      formatter: (ps: { dataIndex: number; axisValue: string }[]) => {
        const i = ps[0].dataIndex
        const d = byDay.value.get(axisDays.value[i])
        return d ? `${ps[0].axisValue}<br/>min ${num(d.hr_min_bpm)} · avg ${num(d.hr_avg_bpm)} · max ${num(d.hr_max_bpm)} bpm` : ps[0].axisValue
      },
    },
    xAxis: dayAxis(axisDays.value),
    yAxis: valueAxis('bpm', { scale: true }),
    series: [
      { type: 'line', name: 'min', data: lo, stack: 'r', lineStyle: { opacity: 0 }, showSymbol: false, symbol: 'none' },
      {
        type: 'line',
        name: 'Min–max',
        data: hi.map((h, i) => (h !== null && lo[i] !== null ? h - (lo[i] as number) : null)),
        stack: 'r',
        lineStyle: { opacity: 0 },
        symbol: 'none',
        areaStyle: { color: color('--c-hr'), opacity: 0.2 },
        itemStyle: { color: tint('--c-hr', 0.45) },
      },
      { type: 'line', name: 'Average', data: col((d) => d.hr_avg_bpm), showSymbol: false, lineStyle: { width: 2, color: color('--c-hr') }, itemStyle: { color: color('--c-hr') } },
    ],
  }
})

const simple = (f: (d: DailyHeart) => number | null, name: string, unit: string, token: string) => ({
  grid: grid(),
  tooltip: axisTooltip(),
  xAxis: dayAxis(axisDays.value),
  yAxis: valueAxis(unit, { scale: true }),
  series: [{ type: 'line', name, data: col(f), connectNulls: true, symbolSize: 5, lineStyle: { width: 2, color: color(token) }, itemStyle: { color: color(token) } }],
})
const vo2Option = computed(() => simple((d) => d.vo2max_ml_kg_min, 'VO2max', 'mL/kg/min', '--c-vo2'))
const spo2Option = computed(() => simple((d) => d.spo2_avg_pct, 'SpO2', '%', '--c-spo2'))
const hasVO2 = computed(() => days.value.some((d) => d.vo2max_ml_kg_min !== null))
const hasSpO2 = computed(() => days.value.some((d) => d.spo2_avg_pct !== null))
const prevAvg = (m: keyof DailyHeart, digits = 0, unit = 'bpm') => {
  if (!state.value.compare || !prev.value.length) return undefined
  const v = avgOf(prev.value, m)
  return v === null ? undefined : `${num(v, digits)} ${unit}`
}

const kpis = computed(() => [
  { label: 'Resting HR', value: num(avgOf(days.value, 'resting_hr_bpm')), unit: 'bpm', delta: delta('resting_hr_bpm'), lowerIsBetter: true,
    previous: prevAvg('resting_hr_bpm'), series: col((d) => d.resting_hr_bpm), color: color('--c-hr') },
  { label: 'HRV', value: num(avgOf(days.value, 'hrv_sdnn_ms')), unit: 'ms', delta: delta('hrv_sdnn_ms'),
    previous: prevAvg('hrv_sdnn_ms', 0, 'ms'), series: col((d) => d.hrv_sdnn_ms), color: color('--c-hrv') },
  { label: 'Walking HR', value: num(avgOf(days.value, 'walking_hr_bpm')), unit: 'bpm', delta: delta('walking_hr_bpm'), lowerIsBetter: true,
    previous: prevAvg('walking_hr_bpm'), series: col((d) => d.walking_hr_bpm), color: color('--c-walk') },
  { label: 'Sleeping HR', value: num(avgOf(days.value, 'hr_sleep_avg_bpm')), unit: 'bpm', delta: delta('hr_sleep_avg_bpm'), lowerIsBetter: true,
    previous: prevAvg('hr_sleep_avg_bpm'), series: col((d) => d.hr_sleep_avg_bpm), color: color('--c-hr') },
])

const strip = computed<StripItem[]>(() => [
  { label: 'VO2max (latest)', value: num(latestVO2.value, 1), unit: 'mL/kg/min', hint: 'from outdoor walks & runs' },
  { label: 'Blood oxygen', value: num(avgOf(days.value, 'spo2_avg_pct'), 1), unit: '%', delta: delta('spo2_avg_pct'), hint: 'SpO2 average' },
  { label: 'Respiratory rate', value: num(avgOf(days.value, 'resp_rate_bpm'), 1), unit: '/min', delta: delta('resp_rate_bpm'), lower: true, hint: 'during sleep' },
  { label: 'Wrist temperature', value: num(avgOf(days.value, 'wrist_temp_dev_c'), 2), unit: '°C', hint: 'vs your 30-night baseline' },
])

const latestVO2 = computed(() => [...days.value].reverse().find((d) => d.vo2max_ml_kg_min !== null)?.vo2max_ml_kg_min ?? null)

const eventLabel: Record<string, string> = { high: 'High heart rate', low: 'Low heart rate', irregular: 'Irregular rhythm' }
const openDay = openDayHandler(router)
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <StateBlock :loading="res.loading.value" :error="res.error.value" :empty="!days.length" :height="320">
      <div class="kpis">
        <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" />
      </div>
      <MetricStrip :items="strip" vs="previous period" />

      <div class="cols-2">
        <Card title="Resting heart rate" subtitle="Daily values, 7-day mean and your usual range (90-day median ± 1 SD)">
          <Chart :option="rhrOption" :height="230" label="Resting heart rate trend" @click="openDay" />
        </Card>
        <Card title="Heart rate variability" subtitle="SDNN, higher usually means better recovery">
          <Chart :option="hrvOption" :height="230" label="Heart rate variability trend" @click="openDay" />
        </Card>
        <Card title="Walking heart rate">
          <Chart :option="walkOption" :height="230" label="Walking heart rate trend" @click="openDay" />
        </Card>
        <Card title="Daily heart rate range">
          <Chart :option="rangeOption" :height="230" label="Daily minimum, average and maximum heart rate" @click="openDay" />
        </Card>
        <Card v-if="hasVO2" title="VO2max" subtitle="Apple estimates it mostly from outdoor walks and runs">
          <Chart :option="vo2Option" :height="200" label="VO2max estimates" />
        </Card>
        <Card v-if="hasSpO2" title="Blood oxygen">
          <Chart :option="spo2Option" :height="200" label="Average blood oxygen per day" />
        </Card>
      </div>

      <Card title="Heart notifications" subtitle="High / low heart rate and irregular rhythm events">
        <StateBlock :loading="events.loading.value" :error="events.error.value" :empty="!events.data.value?.items.length"
          empty-text="No heart notifications in this period.">
          <ul class="events">
            <li v-for="e in events.data.value?.items" :key="e.start_ms">
              <span class="chip attention">{{ eventLabel[e.type] ?? e.type }}</span>
              <span class="tnum">{{ dateTime(e.start_ms) }}</span>
              <span v-if="e.metadata?.HKHeartRateEventThreshold" class="muted small">threshold {{ e.metadata.HKHeartRateEventThreshold }}</span>
            </li>
          </ul>
        </StateBlock>
      </Card>
      <p class="muted small">Ritm shows trends for information only — it is not medical advice.</p>
    </StateBlock>
  </div>
</template>

<style scoped>
.events {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.events li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
</style>
