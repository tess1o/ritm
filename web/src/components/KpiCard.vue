<script setup lang="ts">
// Headline metric: value + delta, the previous value, a sparkline and optional goal progress.
import { computed } from 'vue'
import DeltaBadge from './DeltaBadge.vue'
import Sparkline from './Sparkline.vue'
import { num } from '../lib/fmt'

const props = defineProps<{
  label: string
  value: string
  unit?: string
  delta?: number | null
  lowerIsBetter?: boolean
  vs?: string
  previous?: string // formatted comparison value
  previousLabel?: string // default "was"
  series?: (number | null)[]
  seriesLabel?: string
  color: string // CSS colour value
  goal?: { current: number | null; target: number; unit?: string }
  footnote?: string
}>()

const pct = computed(() => {
  const g = props.goal
  if (!g || g.current === null || !g.target) return null
  return Math.round((g.current / g.target) * 100)
})
</script>

<template>
  <article class="kpi" :style="{ '--kpi': color }">
    <header>
      <span class="label">{{ label }}</span>
      <DeltaBadge v-if="delta !== undefined" :delta="delta" :lower-is-better="lowerIsBetter" :vs="vs ?? 'previous period'" />
    </header>
    <div class="value tnum">{{ value }}<span v-if="unit" class="unit">{{ unit }}</span></div>
    <div class="sub small">
      <span v-if="previous" class="muted">{{ previousLabel ?? 'was' }} {{ previous }}</span>
      <span v-else-if="footnote" class="muted">{{ footnote }}</span>
    </div>
    <Sparkline v-if="series" :values="series" :color="color" :label="`${label}: ${seriesLabel ?? 'daily trend'}`" />
    <div v-if="goal" class="goal">
      <div class="track" :aria-label="`${pct ?? 0} % of goal`"><div class="fill" :style="{ width: `${Math.min(100, pct ?? 0)}%` }" /></div>
      <span class="small text-2 tnum">{{ pct ?? '–' }} % of {{ num(goal.target) }}{{ goal.unit ? ` ${goal.unit}` : '' }} goal</span>
    </div>
  </article>
</template>

<style scoped>
.kpi {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  padding: 14px 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  position: relative;
  overflow: hidden;
}
.kpi::before {
  content: '';
  position: absolute;
  inset: 0 0 auto 0;
  height: 3px;
  background: var(--kpi);
  opacity: 0.85;
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.label {
  font-size: 0.82rem;
  font-weight: 560;
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.value {
  font-size: 1.9rem;
  font-weight: 680;
  letter-spacing: -0.02em;
  line-height: 1.1;
}
.unit {
  margin-left: 4px;
  font-size: 0.9rem;
  font-weight: 500;
  color: var(--muted);
}
.sub {
  min-height: 18px;
}
.goal {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.track {
  height: 6px;
  border-radius: 999px;
  background: var(--elevated);
  overflow: hidden;
}
.fill {
  height: 100%;
  border-radius: 999px;
  background: var(--kpi);
}
</style>
