import { computed, type Ref } from 'vue'
import type { Router } from 'vue-router'
import { addDays } from '../lib/fmt'

/** Every day of the range, so chart x-axes stay continuous when days have no data. */
export function useDayAxis(range: Ref<{ from: string; to: string }>) {
  return computed(() => {
    const out: string[] = []
    for (let d = range.value.from; d <= range.value.to; d = addDays(d, 1)) out.push(d)
    return out
  })
}

/** Chart click handler that opens the clicked day in Day view. */
export function openDayHandler(router: Router) {
  return (p: unknown) => {
    const name = (p as { name?: string })?.name
    if (name) router.push(`/day/${name}`)
  }
}
