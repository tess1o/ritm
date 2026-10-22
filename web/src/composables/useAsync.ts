import { onScopeDispose, ref, shallowRef, watch, type WatchSource } from 'vue'

/** Run an async loader whenever sources change; cancels stale requests. */
export function useAsync<T>(loader: (signal: AbortSignal) => Promise<T>, sources: WatchSource[] = []) {
  const data = shallowRef<T | null>(null)
  const error = ref<string | null>(null)
  const loading = ref(false)
  let ctrl: AbortController | null = null

  async function run() {
    ctrl?.abort()
    const c = new AbortController()
    ctrl = c
    loading.value = true
    error.value = null
    try {
      const v = await loader(c.signal)
      if (!c.signal.aborted) data.value = v
    } catch (e) {
      if (!c.signal.aborted && (e as Error).name !== 'AbortError') error.value = (e as Error).message
    } finally {
      if (ctrl === c) loading.value = false
    }
  }
  watch(sources, run, { immediate: true, deep: true })
  onScopeDispose(() => ctrl?.abort())
  return { data, error, loading, reload: run }
}
