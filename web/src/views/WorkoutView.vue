<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import Icon from '../components/Icon.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { useToast } from '../composables/useToast'
import { api, get } from '../lib/api'
import { clock, loadModelName, longDay, num, time } from '../lib/fmt'
import type { HRPoint, Items, WorkoutDetail, WorkoutRoute } from '../lib/types'
import { workoutIcon } from '../lib/workouts'
import { color, grid, legend } from '../charts/helpers'

const route = useRoute()
const toast = useToast()
const id = computed(() => Number(route.params.id))
const detail = useAsync((s) => get<WorkoutDetail>(`/workouts/${id.value}`, s), [id])
const hr = useAsync((s) => get<Items<HRPoint>>(`/workouts/${id.value}/heart-rate?points=400`, s), [id])
// Leaflet is only downloaded for workouts that have a GPS route.
const RouteMap = defineAsyncComponent(() => import('../components/RouteMap.vue'))
const routeData = useAsync(
  (s) => (detail.data.value?.has_route ? get<WorkoutRoute>(`/workouts/${id.value}/route?points=600`, s) : Promise.resolve(null)),
  [() => detail.data.value?.id, () => detail.data.value?.has_route],
)
const routeSubtitle = computed(() => {
  const r = routeData.data.value
  if (!r) return ''
  return [`${num(r.distance_m / 1000, 2)} km`, r.elevation_gain_m ? `+${num(r.elevation_gain_m)} m climb` : '', 'coloured by pace']
    .filter(Boolean).join(' · ')
})
const compare = ref(false)
const prevHR = useAsync(
  (s) => {
    const p = detail.data.value?.previous_same_type_id
    return compare.value && p ? get<Items<HRPoint>>(`/workouts/${p}/heart-rate?points=400`, s) : Promise.resolve(null)
  },
  [compare, () => detail.data.value?.previous_same_type_id],
)

const w = computed(() => detail.data.value)
setPageMeta(() => {
  const x = w.value
  if (!x) return { back: { to: '/training', label: 'Training' } }
  const where = x.indoor ? 'indoor' : x.indoor === 0 ? 'outdoor' : ''
  return {
    title: x.name,
    icon: workoutIcon(x.activity_type),
    subtitle: [longDay(x.day), `${time(x.start_ms)}–${time(x.end_ms)}`, where, x.source].filter(Boolean).join(' · '),
    back: { to: '/training', label: 'Training' },
  }
})
// The previous session of the same type is the comparison for this workout's numbers.
const before = useAsync(
  (s) => {
    const p = detail.data.value?.previous_same_type_id
    return p ? get<WorkoutDetail>(`/workouts/${p}`, s) : Promise.resolve(null)
  },
  [() => detail.data.value?.previous_same_type_id],
)
const VS = 'previous session'
function change(cur: number | null | undefined, prev: number | null | undefined) {
  if (!before.data.value) return undefined
  return cur != null && prev ? ((cur - prev) / prev) * 100 : null
}
const kpis = computed(() => {
  const x = w.value
  if (!x) return []
  const p = before.data.value
  return [
    { label: 'Duration', value: clock(x.duration_s), delta: change(x.duration_s, p?.duration_s), previous: p ? clock(p.duration_s) : undefined,
      color: color('--c-exercise'), footnote: `moving ${clock(x.moving_s)}` },
    { label: 'Avg heart rate', value: num(x.avg_hr_bpm), unit: 'bpm', delta: change(x.avg_hr_bpm, p?.avg_hr_bpm),
      previous: p?.avg_hr_bpm != null ? `${num(p.avg_hr_bpm)} bpm` : undefined,
      series: hr.data.value?.items.map((h) => h.avg_bpm), seriesLabel: 'heart rate during the workout', color: color('--c-hr') },
    { label: 'Active energy', value: num(x.energy_kcal), unit: 'kcal', delta: change(x.energy_kcal, p?.energy_kcal),
      previous: p?.energy_kcal != null ? `${num(p.energy_kcal)} kcal` : undefined, color: color('--c-active') },
    { label: 'Time in zone 3+', value: num(x.pct_z3_plus), unit: '%', delta: change(x.pct_z3_plus, p?.pct_z3_plus),
      previous: p?.pct_z3_plus != null ? `${num(p.pct_z3_plus)} %` : undefined, color: color('--zone4'),
      footnote: 'share of the session at moderate-hard effort' },
  ]
})
const strip = computed<StripItem[]>(() => {
  const x = w.value
  if (!x) return []
  const p = before.data.value
  const items: StripItem[] = [
    { label: 'Max heart rate', value: num(x.max_hr_bpm), unit: 'bpm', delta: change(x.max_hr_bpm, p?.max_hr_bpm), hint: `${num((x.max_hr_bpm ?? 0) / x.max_hr_bpm_setting * 100)} % of max` },
    { label: 'Training load', value: num(x.load), delta: change(x.load, p?.load), hint: loadHint(x) },
    { label: 'HR recovery (60 s)', value: num(x.hrr60_bpm), unit: 'bpm', hint: 'drop after the end; higher is better' },
  ]
  if (x.distance_m) items.push({ label: 'Distance', value: num(x.distance_m / 1000, 2), unit: 'km', hint: x.pace_s_per_km ? `${clock(x.pace_s_per_km)} /km` : undefined })
  else if (x.srpe_load !== null) items.push({ label: 'sRPE load', value: num(x.srpe_load), hint: 'RPE × minutes' })
  else items.push({ label: 'Burn rate', value: num(x.kcal_per_min, 1), unit: 'kcal/min' })
  return items
})
function loadHint(x: WorkoutDetail) {
  const name = loadModelName[x.load_model] ?? x.load_model
  if (x.load_model === 'srpe' && x.srpe_load === null) return `${name}, estimated from TRIMP (no RPE yet)`
  return x.load_model === 'banister' ? `${name} · Edwards ${num(x.trimp_edwards)}` : `${name} · Banister ${num(x.trimp_banister)}`
}
const zoneTokens = ['--zone0', '--zone1', '--zone2', '--zone3', '--zone4', '--zone5']

const hrOption = computed(() => {
  const d = w.value
  const pts = hr.data.value?.items ?? []
  if (!d) return {}
  const mins = (t: number, start: number) => Math.round(((t - start) / 60000) * 10) / 10
  const bounds = d.zone_lower_bpm
  const zoneAreas = bounds.map((lo, i) => [
    { yAxis: lo, itemStyle: { color: color(zoneTokens[i + 1]), opacity: 0.08 } },
    { yAxis: i < 4 ? bounds[i + 1] : Math.max(d.max_hr_bpm_setting, (d.max_hr_bpm ?? 0) + 5) },
  ])
  const pauseAreas = d.pauses_ms.map(([a, b]) => [
    { xAxis: mins(a, d.start_ms), itemStyle: { color: color('--muted'), opacity: 0.18 } },
    { xAxis: mins(b, d.start_ms) },
  ])
  const series: unknown[] = [
    {
      type: 'line',
      name: 'This session',
      data: pts.map((p) => [mins(p.t_ms, d.start_ms), p.avg_bpm]),
      showSymbol: false,
      lineStyle: { width: 2, color: color('--c-hr') },
      itemStyle: { color: color('--c-hr') },
      markArea: { silent: true, data: [...zoneAreas, ...pauseAreas] },
      markLine: {
        silent: true,
        symbol: 'none',
        lineStyle: { color: color('--muted'), type: 'dashed' },
        label: { formatter: 'end', color: color('--muted') },
        data: [{ xAxis: mins(d.end_ms, d.start_ms) }],
      },
    },
  ]
  const p = before.data.value
  if (compare.value && p && prevHR.data.value) {
    series.push({
      type: 'line',
      name: `Previous (${longDay(p.day)})`,
      data: prevHR.data.value.items.map((x) => [mins(x.t_ms, p.start_ms), x.avg_bpm]),
      showSymbol: false,
      lineStyle: { width: 1.5, color: color('--c-context'), type: 'dashed' },
      itemStyle: { color: color('--c-context') },
    })
  }
  return {
    grid: grid(),
    legend: legend(),
    tooltip: { trigger: 'axis', triggerOn: 'mousemove|click', valueFormatter: (v: number) => `${num(v)} bpm` },
    xAxis: { type: 'value', name: 'min', min: 0, nameGap: 6 },
    yAxis: { type: 'value', name: 'bpm', scale: true, min: (v: { min: number }) => Math.max(40, Math.floor(v.min / 10) * 10 - 10) },
    series,
  }
})

const zonesOption = computed(() => {
  const z = w.value?.zones ?? []
  return {
    grid: grid({ left: 4 }),
    tooltip: { trigger: 'item', triggerOn: 'mousemove|click', formatter: (p: { name: string; value: number }) => `${p.name}: ${clock(p.value * 60)}` },
    xAxis: { type: 'value', name: 'min' },
    yAxis: {
      type: 'category',
      inverse: true,
      data: z.map((x) => (x.zone === 0 ? `Below Z1 <${num(w.value?.zone_lower_bpm[0])}` : `Z${x.zone} ≥${num(x.lower_bpm)}`)),
    },
    series: [
      {
        type: 'bar',
        data: z.map((x) => ({ value: Math.round((x.seconds / 60) * 10) / 10, itemStyle: { color: color(zoneTokens[x.zone]) } })),
        barMaxWidth: 18,
        label: { show: true, position: 'right', color: color('--text-2'), formatter: (p: { value: number }) => `${num(p.value)} min` },
      },
    ],
  }
})

// Zone time only covers stretches with HR samples (each holds ≤ 30 s), so name the gap and where zone 1 starts.
const zonesSubtitle = computed(() => {
  const x = w.value
  if (!x?.zones?.length) return undefined
  const parts = [`Zone 1 starts at ${num(x.zone_lower_bpm[0])} bpm (Settings → Heart-rate zones)`]
  const covered = x.zones.reduce((a, z) => a + z.seconds, 0)
  const gap = (x.duration_s ?? 0) - covered
  if (gap >= 60) parts.push(`${num(gap / 60)} min without heart-rate samples`)
  return parts.join(' · ')
})

// annotation editor
const rpe = ref<number | null>(null)
const notes = ref('')
const saving = ref(false)
watch(w, (d) => {
  rpe.value = d?.rpe ?? null
  notes.value = d?.notes ?? ''
}, { immediate: true })
async function save() {
  saving.value = true
  try {
    detail.data.value = await api<WorkoutDetail>(`/workouts/${id.value}/annotation`, {
      method: 'PUT',
      body: { rpe: rpe.value, notes: notes.value },
    })
    toast.show('Saved', 'success')
  } catch (e) {
    toast.show((e as Error).message, 'error')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <StateBlock :loading="detail.loading.value" :error="detail.error.value" :empty="!w" :height="320">
      <template v-if="w">
        <Teleport defer to="#topbar-actions">
          <RouterLink :to="`/day/${w.day}`" class="btn small"><Icon name="calendar" :size="16" /> Day view</RouterLink>
        </Teleport>
        <div class="kpis">
          <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" :vs="VS" previous-label="last time" />
        </div>
        <MetricStrip :items="strip" :vs="VS" />

        <Card v-if="w.has_route" title="Route" :subtitle="routeSubtitle">
          <StateBlock :loading="routeData.loading.value" :error="routeData.error.value" :empty="!routeData.data.value?.track.length"
            empty-text="No GPS points stored for this workout." :height="300">
            <RouteMap v-if="routeData.data.value" :route="routeData.data.value" :pauses="w.pauses_ms" />
          </StateBlock>
        </Card>

        <Card title="Heart rate" subtitle="Zone bands from your settings; grey: paused">
          <template #actions>
            <label v-if="w.previous_same_type_id" class="check small">
              <input v-model="compare" type="checkbox" /> Compare with previous
            </label>
          </template>
          <StateBlock :loading="hr.loading.value" :error="hr.error.value" :empty="!hr.data.value?.items.length"
            empty-text="No heart-rate samples recorded for this workout.">
            <Chart :option="hrOption" :height="280" label="Heart rate during the workout" />
          </StateBlock>
        </Card>

        <div class="cols-2">
          <Card title="Time in zones" :subtitle="zonesSubtitle">
            <StateBlock :empty="!w.zones?.length" empty-text="No zone data.">
              <Chart :option="zonesOption" :height="220" label="Minutes in each heart rate zone" />
            </StateBlock>
          </Card>
          <Card title="How did it feel?" subtitle="RPE and notes are kept across re-imports">
            <div class="field">
              <span>RPE (1 = very easy, 10 = maximal)</span>
              <div class="rpe" role="radiogroup" aria-label="RPE">
                <button v-for="n in 10" :key="n" type="button" role="radio" :aria-checked="rpe === n" :class="{ on: rpe === n }"
                  @click="rpe = rpe === n ? null : n">{{ n }}</button>
              </div>
            </div>
            <label class="field">
              <span>Notes</span>
              <textarea v-model="notes" class="input" maxlength="4000" placeholder="e.g. legs day, new squat PR" />
            </label>
            <div><button class="btn primary" type="button" :disabled="saving" @click="save">{{ saving ? 'Saving…' : 'Save' }}</button></div>
          </Card>
        </div>
      </template>
    </StateBlock>
  </div>
</template>

<style scoped>
.rpe {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 6px;
}
.rpe button {
  min-height: var(--tap);
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-strong);
  background: var(--bg);
  cursor: pointer;
  font-weight: 600;
}
.rpe button.on {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent);
}
@media (min-width: 640px) {
  .rpe {
    grid-template-columns: repeat(10, minmax(0, 1fr));
  }
}
</style>
