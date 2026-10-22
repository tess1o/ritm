import { computed, ref } from 'vue'
import { get } from '../lib/api'
import { ago } from '../lib/fmt'
import type { Freshness } from '../lib/types'

const data = ref<Freshness | null>(null)
let last = 0

/** Data freshness for the app chrome; refreshed at most once a minute. */
export function useFreshness() {
  async function load(force = false) {
    if (!force && Date.now() - last < 60_000) return
    last = Date.now()
    try {
      data.value = await get<Freshness>('/freshness')
    } catch {
      last = 0
    }
  }
  const label = computed(() => (data.value?.last_data_at ? `Last data ${ago(data.value.last_data_at)}` : 'No data yet'))
  const stale = computed(() => !data.value?.last_data_at || data.value.stale)
  return { data, label, stale, load }
}
