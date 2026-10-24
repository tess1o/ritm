<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import Icon from '../components/Icon.vue'
import InsightItem from '../components/InsightItem.vue'
import KpiCard from '../components/KpiCard.vue'
import StateBlock from '../components/StateBlock.vue'
import StreaksCard from '../components/StreaksCard.vue'
import YearHeatmap from '../components/YearHeatmap.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { get, qs } from '../lib/api'
import { addDays, ago, hm, mean, num, parseDay, range, today } from '../lib/fmt'
import type { DailyActivity, DailyHeart, DataCalendar, Overview, RangeItems, SleepNight, Stat, TrainingLoad, Weekly } from '../lib/types'
import { color } from '../charts/helpers'

const router = useRouter()
const ov = useAsync((s) => get<Overview>('/overview?period=last_7d', s))

// Sparklines show the long view: twelve 7-day blocks ending today, the last being this week.
const WEEKS = 12
const trendFrom = addDays(today(), -7 * WEEKS + 1)
const block = new Map(Array.from({ length: 7 * WEEKS }, (_, i) => [addDays(trendFrom, i), Math.floor(i / 7)]))
const daily = useAsync((s) => get<RangeItems<DailyActivity>>(`/activity/daily${qs({ from: trendFrom, hours: false })}`, s))
const heart = useAsync((s) => get<RangeItems<DailyHeart>>(`/heart/daily${qs({ from: trendFrom })}`, s))
const sleep = useAsync((s) => get<RangeItems<SleepNight>>(`/sleep/nights${qs({ from: trendFrom })}`, s))
const load = useAsync((s) => get<TrainingLoad>(`/training/load${qs({ from: trendFrom })}`, s))

// 12 months of days for the heatmap, starting on a Monday so the first column is a full week.
const calTo = today()
const yearAgo = addDays(calTo, -364)
const calFrom = addDays(yearAgo, -((parseDay(yearAgo).getDay() + 6) % 7))
const cal = useAsync((s) => get<DataCalendar>(`/calendar${qs({ from: calFrom, to: calTo })}`, s))
// The last 8 training weeks; the streak counts are over the whole history.
const training = useAsync((s) => get<Weekly>(`/training/weekly${qs({ from: addDays(calTo, -7 * 7) })}`, s))

const VS = 'your 4-week baseline'
const fmtBase = (st: Stat, digits = 0, unit = '') => (st.baseline === null ? undefined : `${num(st.baseline, digits)}${unit ? ` ${unit}` : ''}`)

/** One point per 7-day block: the mean of the daily values, or their sum. */
function weekly<T extends { day: string }>(rows: T[] | undefined, f: (r: T) => number | null, sum = false) {
  if (!rows) return undefined
  const buckets = Array.from({ length: WEEKS }, () => [] as (number | null)[])
  for (const r of rows) {
    const i = block.get(r.day)
    if (i !== undefined) buckets[i].push(f(r))
  }
  return buckets.map((b) => (sum ? b.reduce<number>((a, v) => a + (v ?? 0), 0) : mean(b)))
}

// One card per area, each opening its page.
const kpis = computed(() => {
  const o = ov.data.value
  if (!o) return []
  const hrv = o.hrv_sdnn_ms
  const heartRows = heart.data.value?.items
  return [
    {
      label: 'Steps / day', to: '/activity', value: num(o.steps_per_day.value), delta: o.steps_per_day.delta_pct, previous: fmtBase(o.steps_per_day),
      series: weekly(daily.data.value?.items, (d) => (d.has_watch_data ? d.steps : null)), seriesLabel: 'weekly average, last 12 weeks',
      color: color('--c-steps'), goal: { current: o.steps_per_day.value, target: o.goals.steps_per_day },
    },
    {
      label: 'Resting HR', to: '/heart', value: num(o.resting_hr_bpm.value), unit: 'bpm', delta: o.resting_hr_bpm.delta_pct, lowerIsBetter: true,
      previous: fmtBase(o.resting_hr_bpm, 0, 'bpm'), detail: o.has_sleep_data && hrv.value !== null ? `HRV ${num(hrv.value)} ms` : undefined,
      series: weekly(heartRows, (d) => d.resting_hr_bpm), seriesLabel: 'weekly average, last 12 weeks', color: color('--c-hr'),
    },
    {
      label: 'Sessions', to: '/training', value: `${o.sessions}`, unit: `of ${num(o.sessions_target)}`,
      footnote: `${o.sessions >= o.sessions_target ? 'target met' : `${num(o.sessions_target - o.sessions)} to go`} · ${o.sessions_label}`,
      detail: load.data.value?.latest?.ctl != null ? `fitness ${num(load.data.value.latest.ctl)}` : undefined,
      series: weekly(load.data.value?.days, (d) => d.sessions, true), seriesLabel: 'sessions per week, last 12 weeks', color: color('--c-exercise'),
    },
    o.has_sleep_data
      ? {
          label: 'Sleep / night', to: '/sleep', value: hm(o.asleep_min_per_night.value), delta: o.asleep_min_per_night.delta_pct,
          previous: o.asleep_min_per_night.baseline === null ? undefined : hm(o.asleep_min_per_night.baseline),
          series: weekly(sleep.data.value?.items, (n) => n.asleep_min), seriesLabel: 'weekly average, last 12 weeks', color: color('--c-sleep'),
        }
      : {
          label: 'HRV', to: '/heart', value: num(hrv.value), unit: 'ms', delta: hrv.delta_pct, previous: fmtBase(hrv, 0, 'ms'),
          series: weekly(heartRows, (d) => d.hrv_sdnn_ms), seriesLabel: 'weekly average, last 12 weeks', color: color('--c-hrv'),
        },
  ]
})

const o = computed(() => ov.data.value)
const noData = computed(() => o.value && !o.value.freshness.last_data_at)
setPageMeta(() => ({ subtitle: o.value ? `${range(o.value.from, o.value.to)} · compared with the 4 weeks before` : undefined }))

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
          Ritm reads the Health export from your iPhone. Connect the iPhone Shortcut once and send each export straight from the
          Health share sheet — or export by hand (Health → profile → Export All Health Data) and upload the <code>export.zip</code>.
        </p>
        <div class="row">
          <RouterLink to="/import?connect=iphone" class="btn primary"><Icon name="phone" /> Connect iPhone</RouterLink>
          <RouterLink to="/import" class="btn"><Icon name="upload" /> Upload export.zip</RouterLink>
        </div>
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
        <StateBlock v-if="cal.loading.value || cal.data.value?.items.length" :loading="cal.loading.value" :empty="!cal.data.value" :height="160">
          <StreaksCard v-if="cal.data.value" :days="cal.data.value.items" :from="calFrom" :to="calTo" :last-day="cal.data.value.last_day ?? null"
            :goal="o.goals.steps_per_day" :window="[o.from, o.to]" :training="training.data.value" />
        </StateBlock>

        <Card title="Activity" subtitle="Last 12 months · steps vs your goal · this week outlined · tap a day">
          <StateBlock :loading="cal.loading.value" :error="cal.error.value" :empty="!cal.data.value?.items.length" :height="150">
            <YearHeatmap :days="cal.data.value?.items ?? []" :from="calFrom" :to="calTo" :goal="o.goals.steps_per_day"
              :highlight="[o.from, o.to]" @select="(d) => router.push(`/day/${d}`)" />
          </StateBlock>
        </Card>

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
      </template>
    </StateBlock>
  </div>
</template>

<style scoped>
.cols-2 {
  align-items: start;
}
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
