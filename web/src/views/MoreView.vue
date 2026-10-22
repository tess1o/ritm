<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { useAuth } from '../composables/useAuth'
import { useSleepAvailable } from '../composables/useSleepAvailable'

const { hasSleep } = useSleepAvailable()
const { logout, version } = useAuth()
</script>

<template>
  <div class="page">
    <nav class="list" aria-label="More">
      <RouterLink to="/day"><Icon name="calendar" /> Day view <Icon name="right" class="chev" /></RouterLink>
      <RouterLink v-if="hasSleep" to="/sleep"><Icon name="moon" /> Sleep & recovery <Icon name="right" class="chev" /></RouterLink>
      <RouterLink to="/insights"><Icon name="bulb" /> Insights <Icon name="right" class="chev" /></RouterLink>
      <RouterLink to="/import"><Icon name="upload" /> Import <Icon name="right" class="chev" /></RouterLink>
      <RouterLink to="/assistant"><Icon name="bot" /> AI assistants <Icon name="right" class="chev" /></RouterLink>
      <RouterLink to="/settings"><Icon name="settings" /> Settings <Icon name="right" class="chev" /></RouterLink>
      <button type="button" @click="logout"><Icon name="logout" /> Sign out</button>
    </nav>
    <p class="muted small">Ritm {{ version }}</p>
  </div>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}
.list a,
.list button {
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 54px;
  padding: 0 16px;
  color: var(--text);
  text-decoration: none;
  border: 0;
  border-top: 1px solid var(--border);
  background: transparent;
  text-align: left;
  font-size: 1rem;
  cursor: pointer;
}
.list > :first-child {
  border-top: 0;
}
.chev {
  margin-left: auto;
  color: var(--muted);
}
.list button {
  color: var(--danger);
}
</style>
