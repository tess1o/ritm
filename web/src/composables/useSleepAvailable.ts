import { computed } from 'vue'
import { useFreshness } from './useFreshness'

// Sleep & recovery is hidden when there is no sleep data (§12, open question 1). The flag comes
// with the freshness summary the app chrome already loads.
export function useSleepAvailable() {
  const fresh = useFreshness()
  const hasSleep = computed(() => fresh.data.value?.has_sleep_data ?? false)
  return { hasSleep, check: (force = false) => fresh.load(force) }
}
