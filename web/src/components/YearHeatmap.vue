<script setup lang="ts">
// GitHub-style day grid: one column per week (Monday first), each day shaded by steps against the
// goal, with a dot on workout days. Highlights a window (e.g. the dashboard's week); picks a day.
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { addDays, longDay, num, parseDay } from '../lib/fmt'
import type { CalendarDay } from '../lib/types'

const props = defineProps<{ days: CalendarDay[]; from: string; to: string; goal: number; highlight?: [string, string] }>()
const emit = defineEmits<{ select: [day: string] }>()

const scroller = ref<HTMLElement | null>(null)
const focusDay = ref(props.to)
watch(() => props.to, (t) => (focusDay.value = t))

const byDay = computed(() => new Map(props.days.map((d) => [d.day, d])))
const dfMonth = new Intl.DateTimeFormat('en-GB', { month: 'short' })
const monthName = (d: string) => dfMonth.format(parseDay(d)).replace('Sept', 'Sep')

/** Weeks from the Monday on or before `from`; days after `to` are null. */
const weeks = computed(() => {
  const start = addDays(props.from, -((parseDay(props.from).getDay() + 6) % 7))
  const out: { start: string; days: (string | null)[]; label: string }[] = []
  for (let w = start; w <= props.to; w = addDays(w, 7)) {
    const days = Array.from({ length: 7 }, (_, i) => addDays(w, i)).map((d) => (d <= props.to ? d : null))
    // Label the week holding the 1st of a month; the first week only if its month has room to show.
    const first = days.find((d) => d && d.endsWith('-01'))
    const label = first ? monthName(first) : !out.length && Number(w.slice(8)) <= 14 ? monthName(w) : ''
    out.push({ start: w, days, label })
  }
  return out
})

// Days without the Watch stay grey unless the phone alone reached the goal.
function level(d: string) {
  const c = byDay.value.get(d)
  if (c?.steps == null) return 0
  const r = c.steps / props.goal
  if (c.watch === false && r < 1) return 0
  return r >= 1 ? 4 : r >= 0.75 ? 3 : r >= 0.5 ? 2 : 1
}
const inWindow = (d: string) => !!props.highlight && d >= props.highlight[0] && d <= props.highlight[1]

function describe(d: string) {
  const c = byDay.value.get(d)
  const parts: string[] = []
  if (c?.steps != null) parts.push(`${num(c.steps)} steps${c.watch === false ? ' (Watch not worn)' : ''}`)
  if (c?.workouts) parts.push(`${c.workouts} workout${c.workouts > 1 ? 's' : ''}`)
  return `${longDay(d)} — ${parts.length ? parts.join(' · ') : 'no data'}`
}

const summary = computed(() => {
  let measured = 0
  let atGoal = 0
  let workoutDays = 0
  for (const d of props.days) {
    if (d.day < props.from || d.day > props.to) continue
    if (d.steps != null) {
      measured++
      if (d.steps >= props.goal) atGoal++
    }
    if (d.workouts) workoutDays++
  }
  return { measured, atGoal, workoutDays }
})

// Arrow keys move within the grid: left/right by week, up/down by day.
function onKey(e: KeyboardEvent) {
  const moves: Record<string, number> = { ArrowLeft: -7, ArrowRight: 7, ArrowUp: -1, ArrowDown: 1 }
  if (!(e.key in moves)) return
  e.preventDefault()
  const next = addDays(focusDay.value, moves[e.key])
  if (next < props.from || next > props.to) return
  focusDay.value = next
  nextTick(() => scroller.value?.querySelector<HTMLElement>(`[data-day="${next}"]`)?.focus())
}

// Phones scroll sideways; open on the latest weeks.
function toEnd() {
  nextTick(() => scroller.value && (scroller.value.scrollLeft = scroller.value.scrollWidth))
}
onMounted(toEnd)
watch(() => props.days, toEnd)
</script>

<template>
  <div class="yh">
    <div ref="scroller" class="scroll">
      <div class="grid" :style="{ '--weeks': weeks.length }" role="group" aria-label="Daily steps for the last 12 months" @keydown="onKey">
        <span class="corner" />
        <span v-for="(w, i) in ['Mon', '', 'Wed', '', 'Fri', '', '']" :key="`wd${i}`" class="wd" aria-hidden="true">{{ w }}</span>
        <template v-for="w in weeks" :key="w.start">
          <span class="month" aria-hidden="true">{{ w.label }}</span>
          <template v-for="(d, i) in w.days" :key="d ?? `${w.start}-${i}`">
            <span v-if="!d" />
            <button v-else type="button" class="cell" :class="[`l${level(d)}`, { win: inWindow(d) }]" :data-day="d"
              :tabindex="d === focusDay ? 0 : -1" :title="describe(d)" :aria-label="describe(d)"
              @focus="focusDay = d" @click="emit('select', d)">
              <i v-if="byDay.get(d)?.workouts" class="dot" />
            </button>
          </template>
        </template>
      </div>
    </div>
    <footer class="small muted">
      <span class="tnum">
        {{ num(summary.atGoal) }} of {{ num(summary.measured) }} days at step goal · {{ num(summary.workoutDays) }} workout days
      </span>
      <span class="legend" aria-hidden="true">
        No Watch <i class="cell l0" />
        <span class="gap" />
        Less
        <i v-for="l in 4" :key="l" class="cell" :class="`l${l}`" />
        Goal
        <span class="key"><i class="dot" /> Workout</span>
      </span>
    </footer>
  </div>
</template>

<style scoped>
.yh {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.scroll {
  overflow-x: auto;
  scrollbar-width: thin;
}
.grid {
  display: grid;
  grid-template-columns: auto repeat(var(--weeks), minmax(9px, 1fr));
  grid-auto-flow: column;
  grid-template-rows: auto repeat(7, auto);
  gap: 3px;
  min-width: 600px;
}
.wd,
.month {
  font-size: 0.68rem;
  line-height: 1;
  color: var(--muted);
  white-space: nowrap;
}
.wd {
  display: flex;
  align-items: center;
  padding-right: 4px;
}
.month {
  /* Let the label run over the following weeks' columns. */
  width: 0;
  padding-bottom: 3px;
}
.cell {
  position: relative;
  display: grid;
  place-items: center;
  aspect-ratio: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 3px;
  cursor: pointer;
}
.cell:hover {
  outline: 1px solid var(--text-2);
}
.cell:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.cell.win {
  box-shadow: inset 0 0 0 1.5px var(--text);
}
.l0 {
  background: var(--elevated);
}
.l1 {
  background: color-mix(in srgb, var(--s-blue) 28%, var(--surface));
}
.l2 {
  background: var(--s-blue-dark);
}
.l3 {
  background: var(--s-blue);
}
.l4 {
  background: var(--s-blue-light);
}
.dot {
  display: block;
  width: 40%;
  max-width: 6px;
  aspect-ratio: 1;
  border-radius: 50%;
  background: var(--c-exercise);
  box-shadow: 0 0 0 1px var(--bg);
}
footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px 16px;
}
.legend {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}
.legend .cell {
  display: inline-block;
  width: 11px;
  cursor: default;
}
.legend .cell:hover {
  outline: 0;
}
.legend .gap {
  width: 8px;
}
.key {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 10px;
}
.key .dot {
  width: 6px;
}
</style>
