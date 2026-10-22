// Thin fetch wrapper (FE-4): JSON, errors, 401 → login redirect, CSRF header. Paths are relative
// to the versioned API base, e.g. api('/overview') → /api/v1/overview (see docs/openapi.yaml).
import type { Import } from './types'

/** Base path of the REST API. */
export const API_BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

const CSRF = { 'X-Ritm-CSRF': '1' }

// Set by main.ts (sends the user to the login page), so this module does not import the router.
let unauthorized: () => void = () => {}

/** Register what happens when a request comes back 401 (the session ended). */
export function setUnauthorizedHandler(fn: () => void) {
  unauthorized = fn
}

function onUnauthorized(path: string) {
  if (!path.startsWith('/auth/')) unauthorized()
}

/** Message of an RFC 9457 problem response (the API's error format). */
function problemMessage(data: unknown, status: number): string {
  const p = data as { detail?: string; title?: string; errors?: { message?: string; location?: string }[] } | undefined
  const first = p?.errors?.find((e) => e.message)
  if (first) return first.location ? `${first.message} (${first.location.replace(/^body\./, '')})` : first.message!
  return p?.detail || p?.title || `HTTP ${status}`
}

export async function api<T>(path: string, opts: { method?: string; body?: unknown; signal?: AbortSignal } = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (method !== 'GET') Object.assign(headers, CSRF)
  let body: BodyInit | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  let res: Response
  try {
    res = await fetch(API_BASE + path, { method, headers, body, signal: opts.signal, credentials: 'same-origin' })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 'Network error — is the server running?')
  }
  if (res.status === 401) onUnauthorized(path)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { detail: text }
    }
  }
  if (!res.ok) throw new ApiError(res.status, problemMessage(data, res.status))
  return data as T
}

export const get = <T>(path: string, signal?: AbortSignal) => api<T>(path, { signal })

/** Query string from an object, skipping empty values. */
export function qs(params: Record<string, string | number | boolean | undefined | null>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

/** Upload a file with progress via XHR (fetch has no upload progress). */
export function uploadFile(
  file: File,
  full: boolean,
  onProgress: (loaded: number, total: number) => void,
): { promise: Promise<Import>; abort: () => void } {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<Import>((resolve, reject) => {
    xhr.open('POST', `${API_BASE}/imports${full ? '?full=true' : ''}`)
    xhr.setRequestHeader('X-Ritm-CSRF', '1')
    xhr.setRequestHeader('X-File-Name', encodeURIComponent(file.name))
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream')
    xhr.upload.onprogress = (e) => onProgress(e.loaded, e.lengthComputable ? e.total : file.size)
    xhr.onload = () => {
      let data: Partial<Import> & { detail?: string } = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        /* ignore */
      }
      if (xhr.status === 401) onUnauthorized('/imports')
      if (xhr.status >= 200 && xhr.status < 300 && data.id) resolve(data as Import)
      else reject(new ApiError(xhr.status, data.id ? `Upload failed (HTTP ${xhr.status})` : problemMessage(data, xhr.status)))
    }
    xhr.onerror = () => reject(new ApiError(0, 'Upload failed — network error'))
    xhr.onabort = () => reject(new ApiError(0, 'Upload cancelled'))
    xhr.send(file)
  })
  return { promise, abort: () => xhr.abort() }
}
