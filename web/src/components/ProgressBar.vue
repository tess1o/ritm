<script setup lang="ts">
defineProps<{ value: number; label: string }>()
</script>

<template>
  <div class="pb" role="progressbar" :aria-valuenow="Math.round(value)" aria-valuemin="0" aria-valuemax="100" :aria-label="label">
    <div class="fill" :style="{ width: `${Math.min(100, Math.max(0, value))}%` }" />
  </div>
</template>

<style scoped>
.pb {
  height: 10px;
  border-radius: 999px;
  background: var(--elevated);
  overflow: hidden;
  border: 1px solid var(--border);
}
.fill {
  height: 100%;
  background: var(--accent);
  transition: width 0.25s ease;
}
</style>
