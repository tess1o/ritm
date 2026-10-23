<script setup lang="ts">
// Drag-and-drop or tap to pick export.zip (works from the iOS Files app).
import { ref } from 'vue'
import Icon from './Icon.vue'

defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{ file: [file: File] }>()
const over = ref(false)

function onDrop(e: DragEvent) {
  over.value = false
  const f = e.dataTransfer?.files?.[0]
  if (f) emit('file', f)
}
function onPick(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (f) emit('file', f)
  ;(e.target as HTMLInputElement).value = ''
}
</script>

<template>
  <label class="drop" :class="{ over, disabled }" @dragover.prevent="over = true" @dragleave="over = false" @drop.prevent="onDrop">
    <input type="file" accept=".zip,.xml,application/zip,text/xml" :disabled="disabled" class="sr-only" @change="onPick" />
    <Icon name="upload" :size="28" />
    <strong>Choose export.zip</strong>
    <span class="muted small">or drop it here · export.xml also works</span>
  </label>
</template>

<style scoped>
.drop {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 150px;
  padding: 20px;
  border: 2px dashed var(--border-strong);
  border-radius: var(--radius);
  background: var(--bg);
  color: var(--text-2);
  cursor: pointer;
  text-align: center;
}
.drop:hover,
.drop.over {
  border-color: var(--accent);
  color: var(--text);
  background: var(--accent-bg);
}
.drop.disabled {
  opacity: 0.5;
  pointer-events: none;
}
.drop:focus-within {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
</style>
