<script setup lang="ts">
import { useToast } from '../composables/useToast'
const { toasts } = useToast()
</script>

<template>
  <div class="toasts" aria-live="polite">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind" role="status">{{ t.text }}</div>
  </div>
</template>

<style scoped>
.toasts {
  position: fixed;
  z-index: 60;
  left: 16px;
  right: 16px;
  bottom: calc(var(--tabbar-h) + var(--safe-bottom) + 12px);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  pointer-events: none;
}
.toast {
  pointer-events: auto;
  max-width: 480px;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
  background: var(--elevated);
  border: 1px solid var(--border-strong);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
}
.toast.error {
  border-color: var(--danger);
}
.toast.success {
  border-color: var(--positive);
}
@media (min-width: 1024px) {
  .toasts {
    bottom: 24px;
    left: auto;
    right: 24px;
    align-items: flex-end;
  }
}
</style>
