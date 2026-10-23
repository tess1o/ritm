<script setup lang="ts">
// Polarity is conveyed by icon + label, not colour alone.
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { Insight } from '../lib/types'
import { range } from '../lib/fmt'

const props = defineProps<{ insight: Insight; compact?: boolean }>()
const meta = computed(
  () =>
    ({
      positive: { icon: 'check', label: 'Positive' },
      neutral: { icon: 'info', label: 'Note' },
      attention: { icon: 'alert', label: 'Attention' },
    })[props.insight.polarity],
)
const kind = computed(
  () => ({ daily: 'Daily', weekly: 'Week', monthly: '28 days', long_term: '13 weeks' })[props.insight.period_kind],
)
</script>

<template>
  <article class="insight" :class="insight.polarity">
    <div class="mark" :title="meta.label">
      <Icon :name="meta.icon" :size="16" :label="meta.label" />
    </div>
    <div class="body">
      <h3>{{ insight.title }}</h3>
      <p class="text-2">{{ insight.body }}</p>
      <div class="meta muted small">
        <span class="chip">{{ insight.category }}</span>
        <span>{{ kind }} · {{ range(insight.period_start, insight.period_end) }}</span>
      </div>
      <slot />
    </div>
  </article>
</template>

<style scoped>
.insight {
  display: flex;
  gap: 12px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
}
.insight:first-child {
  border-top: 0;
  padding-top: 0;
}
.mark {
  flex: none;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--neutral-bg);
  color: var(--neutral);
}
.positive .mark {
  background: var(--positive-bg);
  color: var(--positive);
}
.attention .mark {
  background: var(--attention-bg);
  color: var(--attention);
}
.body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  flex: 1;
}
.meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}
</style>
