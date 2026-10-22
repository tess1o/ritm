<script setup lang="ts">
// Loading / error / empty placeholder for a section.
defineProps<{ loading?: boolean; error?: string | null; empty?: boolean; emptyText?: string; height?: number }>()
</script>

<template>
  <div v-if="error" class="error-box small" role="alert">{{ error }}</div>
  <div v-else-if="loading && empty !== false" class="skeleton" :style="{ height: `${height ?? 120}px` }" aria-busy="true" />
  <div v-else-if="empty" class="empty muted small">
    <slot name="empty">{{ emptyText ?? 'No data yet for this period.' }}</slot>
  </div>
  <slot v-else />
</template>

<style scoped>
.skeleton {
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--surface), var(--elevated), var(--surface));
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite;
}
.empty {
  padding: 18px 0;
  text-align: center;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
</style>
