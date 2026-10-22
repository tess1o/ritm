<script setup lang="ts">
// Sticky bar that appears while a settings section has unsaved edits. Sits inside the section's <form>.
defineProps<{ dirty: boolean; saving: boolean }>()
defineEmits<{ discard: [] }>()
</script>

<template>
  <Transition name="bar">
    <div v-if="dirty || saving" class="savebar" role="status">
      <span class="small">{{ saving ? 'Saving and recomputing…' : 'Unsaved changes' }}</span>
      <span class="spacer" />
      <button class="btn small ghost" type="button" :disabled="saving" @click="$emit('discard')">Discard</button>
      <button class="btn small primary" type="submit" :disabled="saving">Save</button>
    </div>
  </Transition>
</template>

<style scoped>
.savebar {
  position: sticky;
  bottom: calc(var(--tabbar-h) + var(--safe-bottom) + 8px);
  z-index: 5;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 8px 8px 14px;
  border-radius: var(--radius);
  border: 1px solid var(--border-strong);
  background: var(--elevated);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
}
.bar-enter-active,
.bar-leave-active {
  transition: opacity 0.15s, transform 0.15s;
}
.bar-enter-from,
.bar-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
@media (min-width: 1024px) {
  .savebar {
    bottom: 16px;
  }
}
</style>
