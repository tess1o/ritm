<script setup lang="ts">
// Date picker that knows the data: a month grid with activity and workout dots, a year view of
// which months have data, and a jump to the last day with data. Picks one day or a range.
import { computed, nextTick, onUnmounted, ref, shallowRef, watch } from 'vue'
import Icon from './Icon.vue'
import { get } from '../lib/api'
import { addDays, dayString, longDay, num, parseDay, today } from '../lib/fmt'
import type { CalendarDay, DataCalendar } from '../lib/types'

const props = withDefaults(
  defineProps<{ mode?: 'day' | 'range'; value?: string; from?: string; to?: string; max?: string; label?: string }>(),
  { mode: 'day', value: undefined, from: undefined, to: undefined, max: undefined, label: 'Choose a date' },
)
const emit = defineEmits<{ pick: [day: string]; 'pick-range': [from: string, to: string] }>()

const open = ref(false)
const view = ref<'month' | 'year'>('month')
const ym = ref('') // month shown, YYYY-MM
const focusDay = ref('')
const anchor = ref<string | null>(null) // range mode: first click
const hover = ref<string | null>(null)
const sheet = ref(false)
const pos = ref({ top: 0, left: 0 })
const trigger = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)

// Per-open cache, so a reopen after an import shows fresh data.
type YearData = { days: Map<string, CalendarDay>; first: string | null; last: string | null }
const years = shallowRef<Record<number, YearData>>({})
const loading = ref(false)
const failed = ref(false)
let ctrl: AbortController | null = null

const maxDay = computed(() => props.max ?? today())
const year = computed(() => Number(ym.value.slice(0, 4)))
const data = computed(() => years.value[year.value])
const extent = computed(() => {
  const any = Object.values(years.value)[0]
  return { first: any?.first ?? null, last: any?.last ?? null }
})
const minYM = computed(() => (extent.value.first ?? maxDay.value).slice(0, 7))
const maxYM = computed(() => maxDay.value.slice(0, 7))

async function load(y: number) {
  if (years.value[y]) return
  ctrl?.abort()
  ctrl = new AbortController()
  loading.value = true
  failed.value = false
  try {
    const res = await get<DataCalendar>(`/calendar?from=${y}-01-01&to=${y}-12-31`, ctrl.signal)
    years.value = { ...years.value, [y]: { days: new Map(res.items.map((d) => [d.day, d])), first: res.first_day ?? null, last: res.last_day ?? null } }
  } catch (e) {
    if ((e as Error).name !== 'AbortError') failed.value = true
  } finally {
    loading.value = false
  }
}
watch([open, year], ([o, y]) => {
  if (o && y) load(y)
})

const ymOf = (d: string) => d.slice(0, 7)
const daysIn = (m: string) => new Date(Number(m.slice(0, 4)), Number(m.slice(5, 7)), 0).getDate()
const shiftYM = (m: string, n: number) => {
  const d = new Date(Number(m.slice(0, 4)), Number(m.slice(5, 7)) - 1 + n, 1)
  return dayString(d).slice(0, 7)
}
const sep = (s: string) => s.replace('Sept', 'Sep')
const dfMonthYear = new Intl.DateTimeFormat('en-GB', { month: 'long', year: 'numeric' })
const dfMonth = new Intl.DateTimeFormat('en-GB', { month: 'short' })

const title = computed(() => (view.value === 'month' ? dfMonthYear.format(parseDay(`${ym.value}-01`)) : String(year.value)))
const canPrev = computed(() => (view.value === 'month' ? ym.value > minYM.value : year.value > Number(minYM.value.slice(0, 4))))
const canNext = computed(() => (view.value === 'month' ? ym.value < maxYM.value : year.value < Number(maxYM.value.slice(0, 4))))
function step(n: number) {
  ym.value = view.value === 'month' ? shiftYM(ym.value, n) : shiftYM(ym.value, 12 * n)
  if (view.value === 'month') focusDay.value = clampDay(`${ym.value}-${focusDay.value.slice(8) || '01'}`)
}
function clampDay(d: string) {
  const m = ymOf(d)
  const n = Math.min(Number(d.slice(8)), daysIn(m))
  const day = `${m}-${String(n).padStart(2, '0')}`
  return day > maxDay.value ? maxDay.value : day
}

/** Month grid aligned to Monday; null = padding. */
const cells = computed(() => {
  const first = `${ym.value}-01`
  const lead = (parseDay(first).getDay() + 6) % 7
  const out: (string | null)[] = Array(lead).fill(null)
  for (let i = 0; i < daysIn(ym.value); i++) out.push(addDays(first, i))
  return out
})

const span = computed<[string, string] | null>(() => {
  if (props.mode === 'day') return props.value ? [props.value, props.value] : null
  if (anchor.value) {
    const h = hover.value ?? anchor.value
    return h < anchor.value ? [h, anchor.value] : [anchor.value, h]
  }
  return props.from && props.to ? [props.from, props.to] : null
})
const isEnd = (d: string) => !!span.value && (d === span.value[0] || d === span.value[1])
const inSpan = (d: string) => !!span.value && d > span.value[0] && d < span.value[1]

function describe(d: string) {
  const c = data.value?.days.get(d)
  if (!c) return `${longDay(d)}: no data`
  const parts: string[] = []
  if (c.activity) parts.push(c.steps != null ? `${num(c.steps)} steps` : 'activity')
  if (c.workouts) parts.push(`${c.workouts} workout${c.workouts > 1 ? 's' : ''}${c.route ? ' (with route)' : ''}`)
  return `${longDay(d)} — ${parts.join(' · ')}`
}

const months = computed(() => {
  const y = year.value
  return Array.from({ length: 12 }, (_, i) => {
    const m = `${y}-${String(i + 1).padStart(2, '0')}`
    let daysWith = 0
    let workouts = 0
    for (const [d, c] of data.value?.days ?? []) {
      if (d.startsWith(m)) {
        daysWith++
        workouts += c.workouts
      }
    }
    return { m, name: sep(dfMonth.format(parseDay(`${m}-01`))), daysWith, workouts, frac: daysWith / daysIn(m), disabled: m > maxYM.value }
  })
})

function pick(d: string) {
  if (d > maxDay.value) return
  if (props.mode === 'day') {
    emit('pick', d)
    close()
    return
  }
  if (!anchor.value) {
    anchor.value = d
    focusDay.value = d
    return
  }
  const [a, b] = d < anchor.value ? [d, anchor.value] : [anchor.value, d]
  emit('pick-range', a, b)
  close()
}
function openMonth(m: string) {
  ym.value = m
  focusDay.value = clampDay(`${m}-${focusDay.value.slice(8) || '01'}`)
  view.value = 'month'
}
function goTo(d: string, select: boolean) {
  view.value = 'month'
  ym.value = ymOf(d)
  focusDay.value = d
  if (select && props.mode === 'day') pick(d)
  else focusCell()
}

function focusCell() {
  nextTick(() => panel.value?.querySelector<HTMLElement>(`[data-day="${focusDay.value}"]`)?.focus())
}
function onGridKey(e: KeyboardEvent) {
  const moves: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
  let next: string | null = null
  if (e.key in moves) next = addDays(focusDay.value, moves[e.key])
  else if (e.key === 'PageUp') next = clampDay(`${shiftYM(ymOf(focusDay.value), -1)}-${focusDay.value.slice(8)}`)
  else if (e.key === 'PageDown') next = clampDay(`${shiftYM(ymOf(focusDay.value), 1)}-${focusDay.value.slice(8)}`)
  if (!next) return
  e.preventDefault()
  if (next > maxDay.value || ymOf(next) < minYM.value) return
  focusDay.value = next
  ym.value = ymOf(next)
  if (anchor.value) hover.value = next
  focusCell()
}

function place() {
  sheet.value = window.matchMedia('(max-width: 639px)').matches
  const r = trigger.value?.getBoundingClientRect()
  if (!r || sheet.value) return
  const w = 320
  pos.value = { top: r.bottom + 6, left: Math.max(8, Math.min(r.left + r.width / 2 - w / 2, window.innerWidth - w - 8)) }
}
function onDocPointer(e: PointerEvent) {
  const t = e.target as Node
  if (!panel.value?.contains(t) && !trigger.value?.contains(t)) close(false)
}
function onDocKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}
function listen(on: boolean) {
  if (on) {
    document.addEventListener('pointerdown', onDocPointer, true)
    document.addEventListener('keydown', onDocKey)
    window.addEventListener('resize', place)
  } else {
    document.removeEventListener('pointerdown', onDocPointer, true)
    document.removeEventListener('keydown', onDocKey)
    window.removeEventListener('resize', place)
  }
}

function show() {
  const start = props.mode === 'day' ? props.value : props.to
  focusDay.value = start && start <= maxDay.value ? start : maxDay.value
  ym.value = ymOf(focusDay.value)
  view.value = 'month'
  anchor.value = hover.value = null
  years.value = {}
  open.value = true
  place()
  listen(true)
  focusCell()
}
function close(refocus = true) {
  if (!open.value) return
  open.value = false
  ctrl?.abort()
  listen(false)
  if (refocus) trigger.value?.focus()
}
onUnmounted(() => listen(false))
</script>

<template>
  <button ref="trigger" class="dc-trigger" type="button" :aria-expanded="open" aria-haspopup="dialog" :aria-label="label"
    @click="open ? close() : show()">
    <slot>{{ label }}</slot>
  </button>
  <Teleport v-if="open" to="body">
    <div v-if="sheet" class="dc-backdrop" />
    <div ref="panel" class="dc-panel" :class="{ sheet }" role="dialog" :aria-label="mode === 'day' ? 'Choose a day' : 'Choose a range'"
      :style="sheet ? undefined : { top: `${pos.top}px`, left: `${pos.left}px` }">
      <header>
        <button class="nav" type="button" :aria-label="view === 'month' ? 'Previous month' : 'Previous year'" :disabled="!canPrev" @click="step(-1)">
          <Icon name="left" :size="16" />
        </button>
        <button class="title" type="button" :aria-label="view === 'month' ? `${title}, show the year` : `${title}, show the month`"
          @click="view = view === 'month' ? 'year' : 'month'">
          {{ title }}
        </button>
        <button class="nav" type="button" :aria-label="view === 'month' ? 'Next month' : 'Next year'" :disabled="!canNext" @click="step(1)">
          <Icon name="right" :size="16" />
        </button>
      </header>

      <p v-if="failed" class="msg">Could not load which days have data.</p>
      <p v-else-if="mode === 'range'" class="msg muted">{{ anchor ? 'Now pick the last day' : 'Pick the first day' }}</p>

      <div v-if="view === 'month'" class="grid" :class="{ loading: loading && !data }" role="group" :aria-label="title" @keydown="onGridKey"
        @pointerleave="hover = null">
        <span v-for="w in ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']" :key="w" class="wd" aria-hidden="true">{{ w }}</span>
        <template v-for="(d, i) in cells" :key="d ?? `pad${i}`">
          <span v-if="!d" />
          <button v-else type="button" class="day tnum" :data-day="d" :tabindex="d === focusDay ? 0 : -1" :disabled="d > maxDay"
            :class="{ has: data?.days.has(d), end: isEnd(d), mid: inSpan(d), today: d === today() }" :title="describe(d)" :aria-label="describe(d)"
            :aria-pressed="isEnd(d)" @click="pick(d)" @pointerenter="anchor && (hover = d)" @focus="focusDay = d">
            {{ parseDay(d).getDate() }}
            <span class="dots" aria-hidden="true">
              <i v-if="data?.days.get(d)?.activity" class="dot act" />
              <i v-if="data?.days.get(d)?.workouts" class="dot wo" />
            </span>
          </button>
        </template>
      </div>

      <div v-else class="months" :class="{ loading: loading && !data }">
        <button v-for="m in months" :key="m.m" type="button" class="month" :class="{ has: m.daysWith, cur: m.m === ymOf(focusDay) }"
          :disabled="m.disabled" @click="openMonth(m.m)">
          <strong>{{ m.name }}</strong>
          <span class="small tnum">{{ m.daysWith ? `${m.daysWith} day${m.daysWith > 1 ? 's' : ''}` : 'No data' }}</span>
          <span class="small tnum">{{ m.workouts ? `${m.workouts} workout${m.workouts > 1 ? 's' : ''}` : '&nbsp;' }}</span>
          <span class="bar" aria-hidden="true"><span :style="{ width: `${Math.round(m.frac * 100)}%` }" /></span>
        </button>
      </div>

      <footer>
        <span class="legend small muted" aria-hidden="true">
          <span><i class="dot act" /> Activity</span>
          <span><i class="dot wo" /> Workout</span>
        </span>
        <span class="actions">
          <button class="btn small ghost" type="button" @click="goTo(today() > maxDay ? maxDay : today(), true)">Today</button>
          <button class="btn small" type="button" :disabled="!extent.last" :title="extent.last ? longDay(extent.last) : 'No data yet'"
            @click="extent.last && goTo(extent.last, true)">Latest data</button>
        </span>
      </footer>
    </div>
  </Teleport>
</template>

<style scoped>
.dc-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  height: 100%;
  padding: 0 6px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.dc-trigger:hover {
  background: var(--elevated);
}
.dc-trigger:focus-visible {
  outline: 1px solid var(--accent);
}
.dc-backdrop {
  position: fixed;
  inset: 0;
  z-index: 59;
  background: rgba(5, 7, 10, 0.7);
}
.dc-panel {
  position: fixed;
  z-index: 60;
  width: 320px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--elevated);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
}
.dc-panel.sheet {
  left: 0;
  right: 0;
  bottom: 0;
  width: auto;
  border-radius: var(--radius) var(--radius) 0 0;
  padding: 16px 16px calc(16px + var(--safe-bottom));
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.nav,
.title {
  height: 36px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-2);
  cursor: pointer;
}
.nav {
  display: grid;
  place-items: center;
  width: 36px;
}
.title {
  padding: 0 10px;
  color: var(--text);
  font: inherit;
  font-weight: 600;
}
.nav:hover:not(:disabled),
.title:hover {
  background: var(--surface);
  color: var(--text);
}
.nav:disabled {
  opacity: 0.35;
  cursor: default;
}
.msg {
  margin: 0;
  font-size: 0.8rem;
  text-align: center;
}
.grid {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: 2px;
}
.loading {
  opacity: 0.6;
}
.wd {
  text-align: center;
  font-size: 0.7rem;
  color: var(--muted);
  padding-bottom: 2px;
}
.day {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 40px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--muted);
  font: inherit;
  font-size: 0.85rem;
  cursor: pointer;
}
.day.has {
  color: var(--text);
  font-weight: 560;
}
.day:hover:not(:disabled, .mid, .end) {
  background: var(--surface);
}
.day.today {
  box-shadow: inset 0 0 0 1px var(--border-strong);
}
.day.mid {
  background: var(--accent-bg);
  border-radius: 0;
}
.day.end {
  background: var(--accent);
  color: #0b1020;
}
.day:disabled {
  opacity: 0.3;
  cursor: default;
}
.day:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.dots {
  display: flex;
  gap: 3px;
  height: 5px;
  margin-top: 2px;
}
.dot {
  display: inline-block;
  width: 5px;
  height: 5px;
  border-radius: 50%;
}
.dot.act {
  background: var(--c-steps);
}
.dot.wo {
  background: var(--c-exercise);
}
.day.end .dot {
  box-shadow: 0 0 0 1px #0b1020;
}
.months {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 6px;
}
.month {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 3px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.month.has {
  color: var(--text);
  background: var(--surface);
}
.month.cur {
  border-color: var(--accent);
}
.month:hover:not(:disabled) {
  border-color: var(--border-strong);
}
.month:disabled {
  opacity: 0.3;
  cursor: default;
}
.month .small {
  font-size: 0.7rem;
  color: var(--text-2);
}
.bar {
  width: 100%;
  height: 3px;
  border-radius: 2px;
  background: var(--border);
  overflow: hidden;
}
.bar span {
  display: block;
  height: 100%;
  background: var(--c-steps);
}
footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.legend {
  display: flex;
  gap: 10px;
}
.legend span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.actions {
  display: flex;
  gap: 6px;
  margin-left: auto;
}
</style>
