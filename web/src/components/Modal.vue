<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import Icon from './Icon.vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLElement | null>(null)
const onKey = (e: KeyboardEvent) => e.key === 'Escape' && emit('close')
onMounted(() => {
  document.addEventListener('keydown', onKey)
  dialog.value?.focus()
})
onUnmounted(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div class="backdrop" @click.self="emit('close')">
      <div ref="dialog" class="dialog" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1">
        <header>
          <h2>{{ title }}</h2>
          <button class="btn ghost icon-btn" type="button" aria-label="Close" @click="emit('close')">
            <Icon name="x" />
          </button>
        </header>
        <slot />
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  background: rgba(5, 7, 10, 0.7);
  display: flex;
  align-items: flex-end;
  justify-content: center;
}
.dialog {
  width: 100%;
  max-width: 560px;
  max-height: 90dvh;
  overflow: auto;
  background: var(--elevated);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius) var(--radius) 0 0;
  padding: 16px 16px calc(16px + var(--safe-bottom));
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.dialog:focus {
  outline: none;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
@media (min-width: 640px) {
  .backdrop {
    align-items: center;
  }
  .dialog {
    border-radius: var(--radius);
    padding: 20px;
  }
}
</style>
