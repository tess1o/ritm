import { computed, ref } from 'vue'
import { API_BASE, api, uploadFile } from '../lib/api'
import type { Import, ImportRow, Progress } from '../lib/types'

const STORE = 'ritm.import.current'

const phase = ref<'idle' | 'uploading' | 'processing' | 'done' | 'failed'>('idle')
const uploadPct = ref(0)
const progress = ref<Progress | null>(null)
const result = ref<ImportRow | null>(null)
const error = ref<string | null>(null)
let abortUpload: (() => void) | null = null
let es: EventSource | null = null
let poll: number | null = null

function remember(id: string | null) {
  try {
    if (id) localStorage.setItem(STORE, id)
    else localStorage.removeItem(STORE)
  } catch {
    /* ignore */
  }
}

function stopWatching() {
  es?.close()
  es = null
  if (poll !== null) clearInterval(poll)
  poll = null
}

function finish(row: ImportRow) {
  stopWatching()
  result.value = row
  phase.value = row.status === 'succeeded' ? 'done' : 'failed'
  error.value = row.error ?? null
  remember(null)
}

async function pollOnce(id: string) {
  try {
    const r = await api<Import>(`/imports/${id}`)
    if (r.progress) progress.value = r.progress
    if (r.status !== 'running' && r.status !== 'queued') finish(r)
  } catch (e) {
    error.value = (e as Error).message
  }
}

/** Follow an import over SSE, falling back to polling (PRG-1, UI-M7). */
function watchImport(id: string) {
  stopWatching()
  phase.value = 'processing'
  remember(id)
  if (typeof EventSource !== 'undefined') {
    es = new EventSource(`${API_BASE}/imports/${id}/events`)
    es.addEventListener('progress', (e) => (progress.value = JSON.parse((e as MessageEvent).data)))
    es.addEventListener('done', (e) => finish(JSON.parse((e as MessageEvent).data)))
    es.onerror = () => {
      // connection dropped (e.g. screen lock) — switch to polling
      es?.close()
      es = null
      if (poll === null) poll = window.setInterval(() => pollOnce(id), 2000)
    }
  } else {
    poll = window.setInterval(() => pollOnce(id), 2000)
  }
}

export function useImport() {
  async function start(file: File, full: boolean) {
    error.value = null
    result.value = null
    progress.value = null
    uploadPct.value = 0
    phase.value = 'uploading'
    const up = uploadFile(file, full, (loaded, total) => (uploadPct.value = total ? (loaded / total) * 100 : 0))
    abortUpload = up.abort
    try {
      const { id } = await up.promise
      watchImport(id)
    } catch (e) {
      phase.value = 'failed'
      error.value = (e as Error).message
    } finally {
      abortUpload = null
    }
  }
  function cancelUpload() {
    abortUpload?.()
  }
  /** Resume showing a running import after a reload or returning to the tab. */
  async function resume() {
    let id: string | null = null
    try {
      id = localStorage.getItem(STORE)
    } catch {
      /* ignore */
    }
    if (!id || phase.value === 'processing') return
    await pollOnce(id)
    if (!result.value) watchImport(id)
  }
  function reset() {
    stopWatching()
    phase.value = 'idle'
    progress.value = null
    result.value = null
    error.value = null
  }
  const busy = computed(() => phase.value === 'uploading' || phase.value === 'processing')
  return { phase, uploadPct, progress, result, error, busy, start, cancelUpload, resume, reset }
}
