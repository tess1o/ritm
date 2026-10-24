<script setup lang="ts">
// A QR code as inline SVG. Always dark on white with a quiet zone, also in dark mode, so phone cameras read it.
import { computed } from 'vue'
import { encode } from 'uqr'

const props = withDefaults(defineProps<{ value: string; label: string; size?: number }>(), { size: 168 })

const qr = computed(() => {
  const { data, size } = encode(props.value, { border: 2 })
  let d = ''
  data.forEach((row, y) => row.forEach((dark, x) => {
    if (dark) d += `M${x} ${y}h1v1h-1z`
  }))
  return { d, size }
})
</script>

<template>
  <svg class="qr" role="img" :aria-label="label" :width="size" :height="size" :viewBox="`0 0 ${qr.size} ${qr.size}`"
    shape-rendering="crispEdges">
    <rect :width="qr.size" :height="qr.size" fill="#fff" />
    <path :d="qr.d" fill="#000" />
  </svg>
</template>

<style scoped>
.qr {
  display: block;
  flex: none;
  border-radius: var(--radius-sm);
}
</style>
