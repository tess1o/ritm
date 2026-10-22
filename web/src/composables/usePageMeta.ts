import { onUnmounted, ref, watchEffect } from 'vue'

// Per-page overrides for the shared top bar (title, subtitle, back link). Pages that only need a
// static title declare it in the route meta instead.
export interface PageMeta {
  title?: string
  subtitle?: string
  back?: { to: string; label: string }
}

const meta = ref<PageMeta>({})

export function usePageMeta() {
  return meta
}

/** Keep the top bar in sync with reactive page data; cleared when the page unmounts. */
export function setPageMeta(source: () => PageMeta) {
  const stop = watchEffect(() => (meta.value = source()))
  onUnmounted(() => {
    stop()
    meta.value = {}
  })
}
