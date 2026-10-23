<script setup lang="ts">
// Delta vs baseline; direction shown by icon and sign, never colour alone.
import { computed } from 'vue'
import Icon from './Icon.vue'
import { num } from '../lib/fmt'

const props = defineProps<{ delta: number | null | undefined; lowerIsBetter?: boolean; neutral?: boolean; threshold?: number; vs?: string }>()
const kind = computed(() => {
  const d = props.delta
  if (d === null || d === undefined || Math.abs(d) < (props.threshold ?? 2) || props.neutral) return 'neutral'
  const good = props.lowerIsBetter ? d < 0 : d > 0
  return good ? 'positive' : 'attention'
})
const icon = computed(() => {
  const d = props.delta ?? 0
  if (Math.abs(d) < (props.threshold ?? 2)) return 'minus'
  return d > 0 ? 'up' : 'down'
})
const label = computed(() => {
  const d = props.delta
  if (d === null || d === undefined) return 'no baseline'
  return `${d > 0 ? '+' : d < 0 ? '−' : ''}${num(Math.abs(d), Math.abs(d) < 10 ? 1 : 0)} %`
})
</script>

<template>
  <span class="chip" :class="kind" :title="`${label} vs ${vs ?? 'baseline'}`">
    <Icon v-if="delta !== null && delta !== undefined" :name="icon" :size="13" />
    <span class="tnum">{{ label }}</span>
  </span>
</template>
