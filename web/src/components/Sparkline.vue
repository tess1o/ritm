<script setup lang="ts">
// Tiny inline-SVG trend line (no chart library); gaps (null) break the line.
import { computed } from 'vue'

const props = withDefaults(defineProps<{ values: (number | null)[]; color: string; height?: number; label: string }>(), { height: 36 })

const W = 100
const MIN_DRAWN = 0.5 // share of slots that must sit on a drawable (≥2-point) run
const geom = computed(() => {
  const v = props.values
  const nums = v.filter((x): x is number => x !== null)
  if (nums.length < 2) return null
  // too gappy to read as a trend (e.g. sleep recorded on scattered nights): show nothing
  const drawn = v.filter((x, i) => x !== null && (v[i - 1] != null || v[i + 1] != null)).length
  if (drawn < v.length * MIN_DRAWN) return null
  const lo = Math.min(...nums)
  const hi = Math.max(...nums)
  const span = hi - lo || 1
  const n = v.length
  const x = (i: number) => (n === 1 ? W / 2 : (i / (n - 1)) * W)
  const y = (val: number) => 3 + (1 - (val - lo) / span) * (props.height - 6)
  const segments: string[] = []
  let cur = ''
  v.forEach((val, i) => {
    if (val === null) {
      if (cur) segments.push(cur)
      cur = ''
      return
    }
    cur += `${cur ? 'L' : 'M'}${x(i).toFixed(2)},${y(val).toFixed(2)}`
  })
  if (cur) segments.push(cur)
  const line = segments.join(' ')
  // area under the longest continuous run for a soft fill
  const area = segments
    .map((s) => {
      const pts = s.slice(1).split('L').map((p) => p.split(',').map(Number))
      if (pts.length < 2) return ''
      return `M${pts[0][0]},${props.height} L${pts.map((p) => p.join(',')).join(' L')} L${pts[pts.length - 1][0]},${props.height} Z`
    })
    .join(' ')
  const lastIdx = v.map((val, i) => (val === null ? -1 : i)).filter((i) => i >= 0).pop() ?? 0
  return { line, area, last: { x: x(lastIdx), y: y(v[lastIdx] as number) } }
})
const gid = `sg-${Math.random().toString(36).slice(2, 9)}`
</script>

<template>
  <svg v-if="geom" class="spark" :viewBox="`0 0 ${W} ${height}`" preserveAspectRatio="none" :style="{ height: `${height}px` }"
    role="img" :aria-label="label">
    <defs>
      <linearGradient :id="gid" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" :stop-color="color" stop-opacity="0.28" />
        <stop offset="1" :stop-color="color" stop-opacity="0" />
      </linearGradient>
    </defs>
    <path :d="geom.area" :fill="`url(#${gid})`" />
    <path :d="geom.line" fill="none" :stroke="color" stroke-width="2" vector-effect="non-scaling-stroke" stroke-linejoin="round"
      stroke-linecap="round" />
  </svg>
  <div v-else class="spark empty" :style="{ height: `${height}px` }" />
</template>

<style scoped>
.spark {
  display: block;
  width: 100%;
  overflow: visible;
}
</style>
