<script setup lang="ts">
// Compact row of secondary metrics: label, value, optional change badge and hint.
import DeltaBadge from './DeltaBadge.vue'

export interface StripItem {
  label: string
  value: string
  unit?: string
  delta?: number | null
  lower?: boolean
  hint?: string
  to?: string
}
defineProps<{ items: StripItem[]; vs?: string }>()
</script>

<template>
  <div class="strip" :style="{ '--cols': Math.min(items.length, 4) }">
    <component :is="it.to ? 'RouterLink' : 'div'" v-for="it in items" :key="it.label" :to="it.to" class="item" :class="{ link: !!it.to }">
      <span class="label">{{ it.label }}</span>
      <span class="value tnum">{{ it.value }}<small v-if="it.unit"> {{ it.unit }}</small></span>
      <span class="foot">
        <DeltaBadge v-if="it.delta !== undefined" :delta="it.delta" :lower-is-better="it.lower" :vs="vs" />
        <span v-if="it.hint" class="muted small hint">{{ it.hint }}</span>
      </span>
    </component>
  </div>
</template>

<style scoped>
.strip {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1px;
  background: var(--border);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}
.item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px 16px;
  min-width: 0;
  background: var(--surface);
  color: var(--text);
  text-decoration: none;
}
.item.link:hover {
  background: var(--elevated);
  text-decoration: none;
}
.label {
  font-size: 0.8rem;
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.value {
  font-size: 1.25rem;
  font-weight: 650;
}
.value small {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--muted);
}
.foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-height: 22px;
}
.hint {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}
@media (min-width: 640px) {
  .strip {
    grid-template-columns: repeat(var(--cols), minmax(0, 1fr));
  }
}
</style>
