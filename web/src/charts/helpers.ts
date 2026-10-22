import { cssVar } from '../lib/css'
import { shortDay, weekday } from '../lib/fmt'

export const isPhone = () => window.matchMedia('(max-width: 639px)').matches

export const color = (token: string) => cssVar(token)

/** Token colour blended toward the card surface (0 = surface, 1 = full colour). Opaque, so overlapping marks stay clean. */
export function tint(token: string, strength: number) {
  const rgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
  const c = rgb(cssVar(token))
  const s = rgb(cssVar('--surface'))
  return `rgb(${c.map((v, i) => Math.round(s[i] + (v - s[i]) * strength)).join(',')})`
}

/** Grid with room for axis labels; tighter on phones (UI-M6). */
export function grid(extra: Record<string, unknown> = {}) {
  return { left: 8, right: 8, top: 28, bottom: 8, containLabel: true, ...extra }
}

/** Category x-axis of days with sparse labels. */
export function dayAxis(days: string[], extra: Record<string, unknown> = {}) {
  const n = days.length
  const every = isPhone() ? Math.ceil(n / 5) : Math.ceil(n / 10)
  return {
    type: 'category',
    data: days,
    axisLabel: {
      interval: Math.max(0, every - 1),
      formatter: (d: string) => (n <= 8 ? weekday(d) : shortDay(d)),
      fontSize: isPhone() ? 11 : 11,
    },
    axisTick: { show: false },
    ...extra,
  }
}

export function valueAxis(name?: string, extra: Record<string, unknown> = {}) {
  return { type: 'value', name, nameGap: 10, splitNumber: isPhone() ? 3 : 4, scale: false, ...extra }
}

export function axisTooltip(formatter?: (params: unknown) => string) {
  return { trigger: 'axis', triggerOn: 'mousemove|click', axisPointer: { type: 'shadow' }, ...(formatter ? { formatter } : {}) }
}

export function goalLine(value: number, label: string) {
  return {
    silent: true,
    symbol: 'none',
    lineStyle: { color: cssVar('--muted'), type: 'dashed' },
    label: { color: cssVar('--muted'), formatter: label, position: 'insideEndTop', fontSize: 11 },
    data: [{ yAxis: value }],
  }
}

export function legend(extra: Record<string, unknown> = {}) {
  return { top: 0, left: 0, itemWidth: 12, itemHeight: 8, icon: 'roundRect', ...extra }
}
