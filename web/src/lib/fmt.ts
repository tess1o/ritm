// Formatting helpers (native Intl only). Metric units throughout.
import { ref } from 'vue'

// The user's time zone (Settings, else RITM_TZ). Local days are computed by the server in it, so
// "today" and clock times are shown in that zone wherever the browser is. Reactive, so computed
// periods update once it is known.
const zone = ref<string | undefined>(undefined)

// Older browsers know some zones only by their previous names.
const ZONE_ALIASES: Record<string, string> = { 'Europe/Kyiv': 'Europe/Kiev' }

/** This browser's IANA time zone, sent at sign-in so a new user starts with it. */
export function browserTimeZone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return undefined
  }
}

/** Use the user's IANA time zone for today() and clock times (falls back to the browser's). */
export function setTimeZone(tz: string | undefined) {
  for (const z of [tz, tz && ZONE_ALIASES[tz]]) {
    if (!z) continue
    try {
      new Intl.DateTimeFormat('en-GB', { timeZone: z })
      zone.value = z
      return
    } catch {
      /* unknown to this browser */
    }
  }
  zone.value = undefined
}

const zonedFormats = new Map<string, Intl.DateTimeFormat>()
/** A formatter for instants (epoch ms) in the user's zone. */
function zoned(opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = `${zone.value}|${JSON.stringify(opts)}`
  let f = zonedFormats.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat('en-GB', { ...opts, timeZone: zone.value })
    zonedFormats.set(key, f)
  }
  return f
}

function zonedParts(ms: number): Record<string, number> {
  const out: Record<string, number> = {}
  for (const p of zoned({ year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(ms)) {
    if (p.type !== 'literal') out[p.type] = Number(p.value)
  }
  return out
}

/** Minutes since local midnight of an instant, in the server's zone. */
export function minutesOfDay(ms: number): number {
  const p = zonedParts(ms)
  return p.hour * 60 + p.minute
}
const nf = new Map<number, Intl.NumberFormat>()
function numberFormat(digits: number) {
  let f = nf.get(digits)
  if (!f) {
    f = new Intl.NumberFormat('en-GB', { maximumFractionDigits: digits, minimumFractionDigits: digits })
    nf.set(digits, f)
  }
  return f
}

export function num(v: number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '–'
  return numberFormat(digits).format(v).replace(/,/g, ' ')
}

export function km(m: number | null | undefined): string {
  return m === null || m === undefined ? '–' : num(m / 1000, 1)
}

/** 415 → "6 h 55 min" */
export function hm(min: number | null | undefined): string {
  if (min === null || min === undefined) return '–'
  const m = Math.round(min)
  const h = Math.floor(m / 60)
  return h > 0 ? `${h} h ${m % 60} min` : `${m} min`
}

/** seconds → "58:30" or "1:02:05" */
export function clock(s: number | null | undefined): string {
  if (s === null || s === undefined) return '–'
  const t = Math.round(s)
  const h = Math.floor(t / 3600)
  const m = Math.floor((t % 3600) / 60)
  const sec = t % 60
  const mm = String(m).padStart(h ? 2 : 1, '0')
  return h ? `${h}:${mm}:${String(sec).padStart(2, '0')}` : `${mm}:${String(sec).padStart(2, '0')}`
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${num(v, v < 10 ? 1 : 0)} ${u[i]}`
}

/** Parse YYYY-MM-DD as a local calendar date (no TZ shift). */
export function parseDay(d: string): Date {
  const [y, m, dd] = d.split('-').map(Number)
  return new Date(y, m - 1, dd)
}

export function dayString(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${dd}`
}

export function addDays(day: string, n: number): string {
  const d = parseDay(day)
  d.setDate(d.getDate() + n)
  return dayString(d)
}

/** The local day of an instant in the server's zone, YYYY-MM-DD. */
export function dayOf(v: string | number): string {
  const p = zonedParts(new Date(v).getTime())
  return `${p.year}-${String(p.month).padStart(2, '0')}-${String(p.day).padStart(2, '0')}`
}

/** Today in the server's zone, YYYY-MM-DD. */
export function today(): string {
  return dayOf(Date.now())
}

const dfShort = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' })
const dfShortYear = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
const dfLong = new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
const dfWeekday = new Intl.DateTimeFormat('en-GB', { weekday: 'short' })

// en-GB abbreviates September as "Sept"; the server's texts (Go "Jan" layout) say "Sep".
const sep = (s: string) => s.replace('Sept', 'Sep')

export const shortDay = (d: string) => sep(dfShort.format(parseDay(d)))
export const shortDayYear = (d: string) => sep(dfShortYear.format(parseDay(d)))
export const longDay = (d: string) => sep(dfLong.format(parseDay(d)))
export const weekday = (d: string) => dfWeekday.format(parseDay(d))
// Instants are formatted in the server's zone; YYYY-MM-DD days above are calendar dates.
export const time = (ms: number) => zoned({ hour: '2-digit', minute: '2-digit' }).format(ms)
export const dateTime = (v: string | number) => sep(zoned({ day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).format(new Date(v)))

const dfMonth = new Intl.DateTimeFormat('en-GB', { month: 'short', year: '2-digit' })
/** "2026-10" → "Oct 26" */
export const monthLabel = (ym: string) => sep(dfMonth.format(parseDay(`${ym}-01`)))

/** "12–18 Oct", "28 Sep – 4 Oct" */
export function range(from: string, to: string): string {
  if (from === to) return shortDay(from)
  if (from.slice(0, 7) === to.slice(0, 7)) return `${parseDay(from).getDate()}–${shortDay(to)}`
  return `${shortDay(from)} – ${shortDay(to)}`
}

/** "3 days ago" */
export function ago(v: string | number | null | undefined): string {
  if (!v) return 'never'
  const diff = Date.now() - new Date(v).getTime()
  const min = Math.round(diff / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min} min ago`
  const h = Math.round(min / 60)
  if (h < 36) return `${h} h ago`
  const d = Math.round(h / 24)
  return `${d} day${d === 1 ? '' : 's'} ago`
}

export function typeName(t: string): string {
  return t.replace(/^HK(Quantity|Category)TypeIdentifier/, '').replace(/([a-z])([A-Z])/g, '$1 $2')
}

export function mean(v: (number | null | undefined)[]): number | null {
  const x = v.filter((n): n is number => n !== null && n !== undefined)
  return x.length ? x.reduce((a, b) => a + b, 0) / x.length : null
}

/** Trailing rolling mean over n points, ignoring nulls. */
export function rolling(v: (number | null)[], n: number): (number | null)[] {
  return v.map((_, i) => {
    const w = v.slice(Math.max(0, i - n + 1), i + 1)
    const m = mean(w)
    return m === null ? null : Math.round(m * 10) / 10
  })
}

/** Short names of the training-load models (Settings → Training). */
export const loadModelName: Record<string, string> = { edwards: 'Edwards TRIMP', banister: 'Banister TRIMP', srpe: 'sRPE' }
