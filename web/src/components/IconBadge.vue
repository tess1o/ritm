<script setup lang="ts">
// An icon in a tinted circle, e.g. the workout type next to a workout's name.
import Icon from './Icon.vue'

withDefaults(defineProps<{ name: string; size?: number; tone?: string }>(), { size: 34, tone: '--c-active' })
</script>

<template>
  <span class="badge" :style="{ width: `${size}px`, height: `${size}px`, '--tone': `var(${tone})` }">
    <Icon :name="name" :size="Math.round(size * 0.53)" />
  </span>
</template>

<style scoped>
.badge {
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 50%;
  color: var(--tone);
  background: color-mix(in srgb, var(--tone) 16%, transparent);
}
</style>
