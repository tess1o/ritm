<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import Chart from '../components/Chart.vue'
import Icon from '../components/Icon.vue'
import InsightItem from '../components/InsightItem.vue'
import KpiCard from '../components/KpiCard.vue'
import MetricStrip, { type StripItem } from '../components/MetricStrip.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { get } from '../lib/api'
import { addDays, ago, hm, km, num, range } from '../lib/fmt'
import type { DailyActivity, DailyHeart, Overview, RangeItems, Stat } from '../lib/types'
import { axisTooltip, color, dayAxis, goalLine, grid, valueAxis } from '../charts/helpers'

const router = useRouter()
const ov = useAsync((s) => get<Overview>('/overview?period=last_7d', s))
const daily = useAsync((s) => get<RangeItems<DailyActivity>>('/activity/daily?period=last_7d&hours=false', s))
const heart = useAsync((s) => get<RangeItems<DailyHeart>>('/heart/daily?period=last_7d', s))

const VS = 'your 4-week baseline'
const fmtBase = (st: Stat, digits = 0, unit = '') => (st.baseline === null ? undefined : `${num(st.baseline, digits)}${unit ? ` ${unit}` : ''}`)

// Daily values for the sparklines, one point per day of the 7-day window.
const week = computed(() => {
  const o = ov.data.value
  if (!o) return []
  const out: string[] = []
  for (let d = o.from; d <= o.to; d = addDays(d, 1)) out.push(d)
  return out
})
function line<T extends { day: string }>(rows: T[] | undefined, f: (r: T) => number | null, watchOnly = false) {
  const m = new Map((rows ?? []).map((r) => [r.day, r]))
  return week.value.map((d) => {
    const r = m.get(d)
    if (!r || (watchOnly && !(r as unknown as DailyActivity).has_watch_data)) return null
    return f(r)
  })
}

const kpis = computed(() => {
  const o = ov.data.value
  if (!o) return []
  const act = daily.data.value?.items
  const hr = heart.data.value?.items
  return [
    {
      label: 'Steps / day', value: num(o.steps_per_day.value), delta: o.steps_per_day.delta_pct, previous: fmtBase(o.steps_per_day),
      series: line(act, (d) => d.steps, true), color: color('--c-steps'), goal: { current: o.steps_per_day.value, target: o.goals.steps_per_day },
    },
    {
      label: 'Active energy / day', value: num(o.active_kcal_per_day.value), unit: 'kcal', delta: o.active_kcal_per_day.delta_pct,
      previous: fmtBase(o.active_kcal_per_day, 0, 'kcal'), series: line(act, (d) => d.active_kcal, true), color: color('--c-active'),
    },
    {
      label: 'Resting HR', value: num(o.resting_hr_bpm.value), unit: 'bpm', delta: o.resting_hr_bpm.delta_pct, lowerIsBetter: true,
      previous: fmtBase(o.resting_hr_bpm, 0, 'bpm'), series: line(hr, (d) => d.resting_hr_bpm), color: color('--c-hr'),
    },
    {
      label: 'HRV', value: num(o.hrv_sdnn_ms.value), unit: 'ms', delta: o.hrv_sdnn_ms.delta_pct,
      previous: fmtBase(o.hrv_sdnn_ms, 0, 'ms'), series: line(hr, (d) => d.hrv_sdnn_ms), color: color('--c-hrv'),
    },
  ]
})

const strip = computed<StripItem[]>(() => {
  const o = ov.data.value
  if (!o) return []
  const items: StripItem[] = [
    { label: 'Exercise / day', value: num(o.exercise_min_per_day.value), unit: 'min', delta: o.exercise_min_per_day.delta_pct, to: '/activity',
      hint: `goal ${num(o.goals.exercise_min)} min` },
    { label: 'Stand hours / day', value: num(o.stand_hours_per_day.value, 1), unit: 'h', delta: o.stand_hours_per_day.delta_pct, to: '/activity',
      hint: `goal ${num(o.goals.stand_hours)} h` },
    { label: 'Gym sessions', value: `${o.sessions}`, unit: `of ${num(o.sessions_target)}`, to: '/training',
      hint: o.sessions >= o.sessions_target ? 'target met' : `${num(o.sessions_target - o.sessions)} to go` },
  ]
  if (o.has_sleep_data) {
    items.push({ label: 'Sleep / night', value: hm(o.asleep_min_per_night.value), delta: o.asleep_min_per_night.delta_pct, to: '/sleep' })
  } else {
    items.push({ label: 'Distance / day', value: km(o.distance_m_per_day.value), unit: 'km', delta: o.distance_m_per_day.delta_pct, to: '/activity' })
  }
  return items
})

const o = computed(() => ov.data.value)
const noData = computed(() => o.value && !o.value.freshness.last_data_at)
setPageMeta(() => ({ subtitle: o.value ? `${range(o.value.from, o.value.to)} · compared with the 4 weeks before` : undefined }))


const stepsOption = computed(() => {
  const d = daily.data.value?.items ?? []
  const goal = o.value?.goals.steps_per_day ?? 8000
  return {
    grid: grid({ top: 16 }),
    tooltip: axisTooltip(),
    xAxis: dayAxis(d.map((x) => x.day)),
    yAxis: valueAxis(),
    series: [
      {
        type: 'bar',
        name: 'Steps',
        data: d.map((x) => ({
          value: x.has_watch_data ? x.steps : null,
          itemStyle: { color: (x.steps ?? 0) >= goal ? color('--positive') : color('--c-steps') },
        })),
        barMaxWidth: 28,
        itemStyle: { borderRadius: [4, 4, 0, 0] },
        markLine: goalLine(goal, `goal ${num(goal)}`),
      },
    ],
  }
})

const scores = computed(() => {
  const s = o.value?.scores
  if (!s) return []
  return [
    { key: 'Activity', score: s.activity },
    { key: 'Heart', score: s.heart },
    { key: 'Training', score: s.training },
  ]
})
</script>

<template>
  <div class="page">
    <StateBlock :loading="ov.loading.value" :error="ov.error.value" :empty="!o" :height="300">
      <Card v-if="noData" title="No data yet">
        <p class="text-2">
          Export your Health data on the iPhone (Health → profile → Export All Health Data), then upload the
          <code>export.zip</code> here.
        </p>
        <div><RouterLink to="/import" class="btn primary"><Icon name="upload" /> Import export.zip</RouterLink></div>
      </Card>

      <template v-else-if="o">
        <RouterLink v-if="o.freshness.stale" to="/import" class="stale-banner" role="status">
          <Icon name="alert" :size="18" />
          <span><strong>Last data {{ ago(o.freshness.last_data_at) }}</strong> — export now</span>
          <Icon name="right" :size="16" />
        </RouterLink>
        <div class="kpis">
          <KpiCard v-for="k in kpis" :key="k.label" v-bind="k" :vs="VS" previous-label="usual" />
        </div>
        <MetricStrip :items="strip" :vs="VS" />

        <div class="cols-2">
          <Card title="Insights" subtitle="Latest findings vs your personal baseline">
            <template #actions>
              <RouterLink to="/insights" class="btn small ghost">All <Icon name="right" :size="16" /></RouterLink>
            </template>
            <StateBlock :empty="!o.insights.length" empty-text="Nothing notable — insights appear once there are a few weeks of data.">
              <div>
                <InsightItem v-for="i in o.insights" :key="i.id" :insight="i" />
              </div>
            </StateBlock>
          </Card>

          <div class="grid">
            <Card title="Steps" subtitle="Last 7 days">
              <StateBlock :loading="daily.loading.value" :error="daily.error.value" :empty="!daily.data.value?.items.length" :height="200">
                <Chart :option="stepsOption" :height="200" label="Daily steps for the last 7 days"
                  @click="(p: any) => p?.name && router.push(`/day/${p.name}`)" />
              </StateBlock>
            </Card>
            <Card v-if="scores.length" title="Scores" subtitle="0–100, display only">
              <div class="scores">
                <div v-for="s in scores" :key="s.key" class="score" :title="s.score.note">
                  <div class="row">
                    <span class="text-2 small">{{ s.key }}</span>
                    <span class="spacer" />
                    <strong class="tnum">{{ s.score.value ?? '–' }}</strong>
                  </div>
                  <div class="bar"><div :style="{ width: `${s.score.value ?? 0}%` }" /></div>
                  <p class="muted small">{{ s.score.note }}</p>
                </div>
              </div>
            </Card>
          </div>
        </div>
      </template>
    </StateBlock>
  </div>
</template>

<style scoped>
.scores {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.score .bar {
  height: 8px;
  border-radius: 999px;
  background: var(--elevated);
  margin: 6px 0 4px;
  overflow: hidden;
}
.score .bar div {
  height: 100%;
  background: var(--accent);
  border-radius: 999px;
}
.stale-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 10px 14px;
  border: 1px solid var(--attention);
  border-radius: var(--radius-sm);
  background: var(--attention-bg);
  color: var(--text);
  text-decoration: none;
}
.stale-banner span {
  flex: 1;
}
.stale-banner > :first-child {
  color: var(--attention);
}
</style>
