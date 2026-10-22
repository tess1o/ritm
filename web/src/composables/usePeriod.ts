import { computed, ref, watch } from 'vue'
import { addDays, today } from '../lib/fmt'

export type PeriodKey = 'week' | 'month' | '3months' | 'year' | 'custom'

const LENGTH: Record<Exclude<PeriodKey, 'custom'>, number> = { week: 7, month: 30, '3months': 91, year: 365 }
const STORE = 'ritm.period.v2' // v2: compare-to-previous defaults to on

function initial(): { key: PeriodKey; from: string; to: string; compare: boolean } {
  try {
    const s = JSON.parse(localStorage.getItem(STORE) ?? 'null')
    if (s && s.key) return s
  } catch {
    /* storage unavailable */
  }
  // shorter default on phones (UI-M6)
  const phone = typeof window !== 'undefined' && window.matchMedia('(max-width: 639px)').matches
  return { key: phone ? 'week' : 'month', from: addDays(today(), -29), to: today(), compare: true }
}

const state = ref(initial())
watch(
  state,
  (v) => {
    try {
      localStorage.setItem(STORE, JSON.stringify(v))
    } catch {
      /* ignore */
    }
  },
  { deep: true },
)

/** Shared period selection used by every screen (§12). */
export function usePeriod() {
  const range = computed(() => {
    const s = state.value
    if (s.key === 'custom') return { from: s.from, to: s.to }
    const t = today()
    return { from: addDays(t, -LENGTH[s.key] + 1), to: t }
  })
  const length = computed(() => {
    const a = new Date(range.value.from).getTime()
    const b = new Date(range.value.to).getTime()
    return Math.round((b - a) / 86400000) + 1
  })
  /** The period of equal length right before the current one. */
  const previous = computed(() => ({
    from: addDays(range.value.from, -length.value),
    to: addDays(range.value.from, -1),
  }))
  function shift(direction: -1 | 1) {
    const n = length.value * direction
    state.value = { ...state.value, key: 'custom', from: addDays(range.value.from, n), to: addDays(range.value.to, n) }
  }
  return { state, range, previous, length, shift }
}
