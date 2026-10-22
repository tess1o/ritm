<script setup lang="ts">
defineProps<{ title?: string; subtitle?: string; flush?: boolean }>()
</script>

<template>
  <section class="card" :class="{ flush }">
    <header v-if="title || $slots.actions" class="card-head">
      <div>
        <h2 v-if="title">{{ title }}</h2>
        <p v-if="subtitle" class="muted small">{{ subtitle }}</p>
      </div>
      <div class="actions"><slot name="actions" /></div>
    </header>
    <slot />
  </section>
</template>

<style scoped>
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 14px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.card.flush {
  padding: 0;
  overflow: hidden;
}
.card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}
.flush .card-head {
  padding: 14px 14px 0;
}
.actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
@media (min-width: 640px) {
  .card {
    padding: 18px;
  }
}
</style>
