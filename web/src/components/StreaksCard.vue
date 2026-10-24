<script setup lang="ts">
// Streaks (daily step goal, weekly training target) with recent-history dots and the distance to
// the record, plus personal records over the calendar's range. Records set in `window` get a badge.
import { computed } from 'vue'
import Icon from './Icon.vue'
import { addDays, longDay, mean, num, range, shortDay } from '../lib/fmt'
import type { CalendarDay, Weekly } from '../lib/types'

const props = defineProps<{
  days: CalendarDay[]
  from: string
  to: string
  lastDay: string | null // last day with data; streaks count back from it so a stale export doesn't zero them
  goal: number
  window: [string, string]
  training: Weekly | null
}>()

const by = computed(() => new Map(props.days.map((d) => [d.day, d])))
const hit = (d: string) => (by.value.get(d)?.steps ?? 0) >= props.goal
const plural = (n: number, w: string) => `${num(n)} ${w}${n === 1 ? '' : 's'}`
const isNew = (d: string) => d >= props.window[0] && d <= props.window[1]

const steps = computed(() => {
  const anchor = props.lastDay && props.lastDay < props.to ? props.lastDay : props.to
  // An unfinished anchor day doesn't break the streak; it counts once the goal is reached.
  let d = hit(anchor) ? anchor : addDays(anchor, -1)
  let current = 0
  for (; d >= props.from && hit(d); d = addDays(d, -1)) current++

  let longest = 0
  let longestEnd = ''
  let run = 0
  let lastHit: string | null = null
  for (let x = props.from; x <= anchor; x = addDays(x, 1)) {
    run = hit(x) ? run + 1 : 0
    if (run) lastHit = x
    if (run > longest) [longest, longestEnd] = [run, x]
  }

  // Each dot is a ring filled by the share of the goal; a day without the Watch stays empty.
  const dots = Array.from({ length: 14 }, (_, i) => {
    const day = addDays(anchor, i - 13)
    const c = by.value.get(day)
    const s = c?.steps ?? null
    const off = !c || c.watch === false
    const state = s !== null && s >= props.goal ? 'hit' : off ? 'off' : 'part'
    const title = `${longDay(day)} — ${s === null ? 'no data' : `${num(s)} steps`}${c?.watch === false ? ' (Watch not worn)' : ''}`
    return { day, state, p: s === null ? 0 : Math.min(1, s / props.goal), title }
  })

  let note: string
  if (current && current >= longest) note = current > 1 ? 'Your longest streak in 12 months — keep it going' : 'Day one — keep it going'
  else if (current) note = `${plural(longest - current + 1, 'more day')} to beat your record of ${longest}`
  else note = lastHit ? `Last at goal ${shortDay(lastHit)} · hit ${num(props.goal)} steps to start again` : `Hit ${num(props.goal)} steps to start one`
  return { current, longest, longestEnd, dots, note, record: current > 0 && current >= longest }
})

const gym = computed(() => {
  const t = props.training
  if (!t || !t.target_sessions) return null
  const weeks = t.weeks.slice(-8).map((w) => ({
    ...w,
    state: w.met ? 'hit' : w.partial ? 'open' : w.sessions ? 'near' : 'miss',
    title: `Week of ${shortDay(w.week_start)} — ${w.sessions} of ${w.target}${w.partial ? ' so far' : ''}`,
  }))
  const now = weeks.at(-1)
  const current = t.current_streak_weeks
  const longest = t.longest_streak_weeks
  let note: string
  if (now?.partial && !now.met) {
    const left = now.target - now.sessions
    note = current ? `${plural(left, 'more session')} this week to make it ${current + 1}` : `${plural(left, 'more session')} this week to start one`
  } else if (current && current >= longest) note = 'Your longest run yet'
  else if (current) note = `${plural(longest - current + 1, 'more week')} to beat your record of ${longest}`
  else note = `Reach ${t.target_sessions} a week to start one`
  return { current, longest, weeks, note, target: t.target_sessions, what: t.session_types.length === 1 ? `${t.target_sessions}× ${t.sessions_label} a week` : `${t.target_sessions} sessions a week · ${t.sessions_label}`, record: current > 0 && current >= longest }
})

// Personal records over the whole range.
const records = computed(() => {
  let bestDay: CalendarDay | null = null
  for (const d of props.days) if (d.steps != null && d.steps > (bestDay?.steps ?? 0)) bestDay = d

  let bestWeek: { start: string; avg: number } | null = null
  let busiest: { start: string; n: number } | null = null
  for (let w = props.from; w <= props.to; w = addDays(w, 7)) {
    const week = Array.from({ length: 7 }, (_, i) => by.value.get(addDays(w, i)))
    const vals = week.map((d) => d?.steps).filter((v): v is number => v != null)
    const avg = mean(vals)
    if (vals.length >= 4 && avg !== null && avg > (bestWeek?.avg ?? 0)) bestWeek = { start: w, avg }
    const n = week.reduce((a, d) => a + (d?.workouts ?? 0), 0)
    if (n > (busiest?.n ?? 0)) busiest = { start: w, n }
  }

  const s = steps.value
  const out: { icon: string; label: string; value: string; when: string; to?: string; fresh: boolean }[] = []
  if (bestDay) out.push({ icon: 'footprints', label: 'Most steps in a day', value: num(bestDay.steps), when: shortDay(bestDay.day), to: `/day/${bestDay.day}`, fresh: isNew(bestDay.day) })
  if (bestWeek) out.push({ icon: 'calendar', label: 'Best week', value: `${num(bestWeek.avg)} / day`, when: `week of ${shortDay(bestWeek.start)}`, fresh: isNew(addDays(bestWeek.start, 6)) || isNew(bestWeek.start) })
  if (s.longest) out.push({ icon: 'flame', label: 'Longest goal streak', value: plural(s.longest, 'day'), when: range(addDays(s.longestEnd, 1 - s.longest), s.longestEnd), fresh: isNew(s.longestEnd) })
  if (busiest) out.push({ icon: 'dumbbell', label: 'Busiest training week', value: plural(busiest.n, 'workout'), when: `week of ${shortDay(busiest.start)}`, to: '/training', fresh: isNew(addDays(busiest.start, 6)) || isNew(busiest.start) })
  return out
})
</script>

<template>
  <div class="streaks">
    <div class="col">
      <section class="streak" :class="{ on: steps.current > 0, record: steps.record }">
        <div class="head">
          <span class="flame"><Icon name="flame" :size="22" /></span>
          <div>
            <div class="big tnum">{{ steps.current }}<small>&nbsp;{{ steps.current === 1 ? 'day' : 'days' }}</small></div>
            <div class="what">at {{ num(goal) }} steps</div>
          </div>
          <span v-if="steps.record" class="badge">Record</span>
        </div>
        <div class="dots" role="list" aria-label="Step goal, last 14 days">
          <i v-for="d in steps.dots" :key="d.day" role="listitem" :class="['dot', d.state]" :style="{ '--p': d.p }" :title="d.title"
            :aria-label="d.title" />
        </div>
        <div class="legend" aria-hidden="true">
          <span><i class="dot hit" /> Goal</span>
          <span><i class="dot part" style="--p: 0.6" /> Share of goal</span>
          <span><i class="dot off" /> No Watch</span>
        </div>
        <p class="note">{{ steps.note }}</p>
      </section>

      <RouterLink v-if="gym" to="/training" class="streak link" :class="{ on: gym.current > 0, record: gym.record }">
        <div class="head">
          <span class="flame gym"><Icon name="dumbbell" :size="22" /></span>
          <div>
            <div class="big tnum">{{ gym.current }}<small>&nbsp;{{ gym.current === 1 ? 'week' : 'weeks' }}</small></div>
            <div class="what">{{ gym.what }}</div>
          </div>
          <span v-if="gym.record" class="badge">Record</span>
        </div>
        <div class="dots weeks" role="list" aria-label="Training target, last 8 weeks">
          <i v-for="w in gym.weeks" :key="w.week_start" role="listitem" :class="['dot', w.state]" :title="w.title" :aria-label="w.title">
            {{ w.sessions }}
          </i>
        </div>
        <p class="note">{{ gym.note }}</p>
      </RouterLink>
    </div>

    <ul class="records">
      <li v-for="r in records" :key="r.label">
        <component :is="r.to ? 'RouterLink' : 'div'" :to="r.to" class="rec" :class="{ link: !!r.to }">
          <span class="ic"><Icon :name="r.icon" :size="18" /></span>
          <span class="txt">
            <span class="label">{{ r.label }}<span v-if="r.fresh" class="badge new">New</span></span>
            <span class="when muted small">{{ r.when }}</span>
          </span>
          <strong class="val tnum">{{ r.value }}</strong>
        </component>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.streaks {
  display: grid;
  gap: var(--gap);
  grid-template-columns: minmax(0, 1fr);
}
.col {
  display: grid;
  gap: var(--gap);
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
}
.streak {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
  text-decoration: none;
}
.streak.link:hover {
  border-color: var(--border-strong);
}
.streak.record {
  border-color: var(--attention);
  background: linear-gradient(160deg, var(--attention-bg), var(--surface) 60%);
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.flame {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: var(--elevated);
  color: var(--muted);
}
.on .flame {
  background: var(--attention-bg);
  color: var(--attention);
}
.on .flame.gym {
  background: var(--positive-bg);
  color: var(--positive);
}
.big {
  font-size: 1.9rem;
  font-weight: 680;
  line-height: 1;
  letter-spacing: -0.02em;
}
.big small {
  font-size: 0.9rem;
  font-weight: 500;
  color: var(--muted);
}
.what {
  margin-top: 3px;
  font-size: 0.8rem;
  color: var(--text-2);
}
.badge {
  margin-left: auto;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--attention-bg);
  color: var(--attention);
  font-size: 0.72rem;
  font-weight: 650;
  letter-spacing: 0.03em;
  text-transform: uppercase;
}
.dots {
  display: grid;
  grid-template-columns: repeat(14, minmax(0, 1fr));
  gap: 4px;
}
.dots.weeks {
  grid-template-columns: repeat(8, minmax(0, 1fr));
}
.dot {
  display: grid;
  place-items: center;
  justify-self: center;
  width: 100%;
  max-width: 22px;
  aspect-ratio: 1;
  border-radius: 50%;
  background: var(--elevated);
  font-style: normal;
  font-size: 0.7rem;
  font-weight: 650;
  color: var(--muted);
}
.dots.weeks .dot {
  max-width: 26px;
}
.dot.hit {
  background: var(--positive);
  color: var(--on-accent);
}
.dot.near {
  box-shadow: inset 0 0 0 2px var(--positive);
  background: transparent;
  color: var(--text-2);
}
.dot.open {
  box-shadow: inset 0 0 0 2px var(--border-strong);
  background: transparent;
  color: var(--text);
}
.dot.part {
  background: conic-gradient(var(--c-steps) calc(var(--p) * 1turn), var(--elevated) 0);
  mask: radial-gradient(circle, transparent 50%, #000 52%);
}
.dot.off {
  background: transparent;
  border: 1.5px dashed var(--border-strong);
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-top: -4px;
  font-size: 0.72rem;
  color: var(--muted);
}
.legend span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.legend .dot {
  width: 10px;
}
.note {
  margin: 0;
  font-size: 0.85rem;
  color: var(--text-2);
}
.records {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  overflow: hidden;
}
.records li {
  display: flex;
  flex: 1;
}
.records li + li {
  border-top: 1px solid var(--border);
}
.rec {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  padding: 11px 16px;
  color: var(--text);
  text-decoration: none;
}
.rec.link:hover {
  background: var(--elevated);
}
.ic {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: none;
  border-radius: 9px;
  background: var(--accent-bg);
  color: var(--accent);
}
.txt {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}
.label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.875rem;
}
.badge.new {
  margin-left: 0;
  background: var(--positive-bg);
  color: var(--positive);
}
.val {
  white-space: nowrap;
}
@media (min-width: 1024px) {
  .streaks {
    grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
  }
}
</style>
