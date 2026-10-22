<script setup lang="ts">
// Apple-style activity rings in a Monday-first calendar: Move (outer), Exercise, Stand (inner).
import { computed } from 'vue'
import { addDays, num, parseDay, shortDay } from '../lib/fmt'
import type { DailyActivity } from '../lib/types'

const props = defineProps<{ days: DailyActivity[]; maxWeeks?: number }>()
const emit = defineEmits<{ select: [day: string] }>()

const RINGS = [
  { key: 'move', label: 'Move', unit: 'kcal', color: 'var(--c-active)', r: 25 },
  { key: 'exercise', label: 'Exercise', unit: 'min', color: 'var(--c-exercise)', r: 18 },
  { key: 'stand', label: 'Stand', unit: 'h', color: 'var(--c-stand)', r: 11 },
] as const
type RingKey = (typeof RINGS)[number]['key']
const STROKE = 6

function values(d: DailyActivity, k: RingKey): [number | null, number | null] {
  const r = d.rings
  if (k === 'move') return [r.move_kcal, r.move_goal_kcal]
  if (k === 'exercise') return [r.exercise_min, r.exercise_goal_min]
  return [r.stand_hours, r.stand_goal_hours]
}
function frac(d: DailyActivity, k: RingKey) {
  const [v, g] = values(d, k)
  return v === null || !g ? 0 : Math.min(1, v / g)
}

const withRings = computed(() => props.days.filter((d) => d.rings.move_goal_kcal !== null))
const shown = computed(() => {
  const list = withRings.value
  if (!list.length) return []
  const weeks = props.maxWeeks ?? 6
  const last = list[list.length - 1].day
  const minDay = addDays(last, -(weeks * 7) + 1)
  return list.filter((d) => d.day >= minDay)
})
const truncated = computed(() => shown.value.length < withRings.value.length)

/** Calendar cells aligned to Monday; null = padding. */
const cells = computed(() => {
  const list = shown.value
  if (!list.length) return []
  const byDay = new Map(list.map((d) => [d.day, d]))
  const first = list[0].day
  const lead = (parseDay(first).getDay() + 6) % 7
  const out: ({ day: string; data: DailyActivity | undefined } | null)[] = Array(lead).fill(null)
  for (let d = first; d <= list[list.length - 1].day; d = addDays(d, 1)) out.push({ day: d, data: byDay.get(d) })
  return out
})

const summary = computed(() =>
  RINGS.map((ring) => ({
    ...ring,
    closed: shown.value.filter((d) => frac(d, ring.key) >= 1).length,
  })),
)

function describe(d: DailyActivity) {
  return `${shortDay(d.day)} — ` + RINGS.map((ring) => {
    const [v, g] = values(d, ring.key)
    return `${ring.label} ${num(v)} of ${num(g)} ${ring.unit}`
  }).join(', ')
}
const circ = (r: number) => 2 * Math.PI * r
</script>

<template>
  <div class="rings-wrap">
    <ul class="legend" aria-label="Ring legend">
      <li v-for="s in summary" :key="s.key">
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
          <circle cx="9" cy="9" r="6" fill="none" :stroke="s.color" stroke-width="4" />
        </svg>
        <span><strong>{{ s.label }}</strong> <span class="muted">closed {{ s.closed }}/{{ shown.length }} days</span></span>
      </li>
    </ul>

    <div class="cal">
      <span v-for="w in ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']" :key="w" class="wd muted">{{ w }}</span>
      <template v-for="(c, i) in cells" :key="i">
        <span v-if="!c" />
        <button v-else type="button" class="cell" :disabled="!c.data" :title="c.data ? describe(c.data) : shortDay(c.day)"
          :aria-label="c.data ? describe(c.data) : `${shortDay(c.day)}: no data`" @click="emit('select', c.day)">
          <svg viewBox="0 0 60 60" class="ring" aria-hidden="true">
            <g v-for="ring in RINGS" :key="ring.key" transform="rotate(-90 30 30)">
              <circle cx="30" cy="30" :r="ring.r" fill="none" :stroke="ring.color" :stroke-width="STROKE" opacity="0.18" />
              <circle v-if="c.data && frac(c.data, ring.key) > 0" cx="30" cy="30" :r="ring.r" fill="none" :stroke="ring.color"
                :stroke-width="STROKE" stroke-linecap="round"
                :stroke-dasharray="`${circ(ring.r) * frac(c.data, ring.key)} ${circ(ring.r)}`" />
            </g>
          </svg>
          <span class="date tnum">{{ parseDay(c.day).getDate() }}</span>
        </button>
      </template>
    </div>
    <p v-if="truncated" class="muted small">Showing the last {{ maxWeeks ?? 6 }} weeks of the period.</p>
  </div>
</template>

<style scoped>
.rings-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.legend {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  font-size: 0.875rem;
}
.legend li {
  display: flex;
  align-items: center;
  gap: 6px;
}
.cal {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: 6px 4px;
  max-width: 560px;
}
.wd {
  text-align: center;
  font-size: 0.72rem;
}
.cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: 2px 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  cursor: pointer;
  min-height: var(--tap);
}
.cell:hover:not(:disabled) {
  background: var(--elevated);
}
.cell:disabled {
  cursor: default;
  opacity: 0.4;
}
.ring {
  width: 100%;
  max-width: 56px;
  aspect-ratio: 1;
}
.date {
  font-size: 0.72rem;
  color: var(--text-2);
}
</style>
