<script setup lang="ts">
import { watch } from 'vue'
import { useRoute } from 'vue-router'
import AppShell from './components/AppShell.vue'
import ToastHost from './components/ToastHost.vue'
import { useAuth } from './composables/useAuth'
import { useSleepAvailable } from './composables/useSleepAvailable'

const route = useRoute()
const { user } = useAuth()
const { hasSleep, check } = useSleepAvailable()
watch(user, (u) => u && check(), { immediate: true })
</script>

<template>
  <RouterView v-if="route.meta.public" />
  <AppShell v-else :has-sleep="hasSleep">
    <RouterView v-slot="{ Component }">
      <component :is="Component" />
    </RouterView>
  </AppShell>
  <ToastHost />
</template>
