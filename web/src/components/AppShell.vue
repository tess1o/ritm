<script setup lang="ts">
// App chrome: grouped sidebar (desktop) or bottom tab bar (phone), plus a shared top bar with the
// page title, per-page actions (teleported into #topbar-actions) and the date presets (UI-M2).
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import Icon from './Icon.vue'
import PeriodPicker from './PeriodPicker.vue'
import { useAuth } from '../composables/useAuth'
import { useFreshness } from '../composables/useFreshness'
import { usePageMeta } from '../composables/usePageMeta'

const props = defineProps<{ hasSleep: boolean }>()
const route = useRoute()
const { user, version, logout } = useAuth()
const fresh = useFreshness()
const pageMeta = usePageMeta()
watch(() => route.fullPath, () => fresh.load(), { immediate: true })

type Item = { to: string; name: string; label: string; icon: string }
const groups = computed<{ label?: string; items: Item[] }[]>(() => [
  { items: [{ to: '/', name: 'dashboard', label: 'Dashboard', icon: 'home' }] },
  {
    label: 'Health',
    items: [
      { to: '/activity', name: 'activity', label: 'Activity', icon: 'footprints' },
      { to: '/heart', name: 'heart', label: 'Heart', icon: 'heart' },
      ...(props.hasSleep ? [{ to: '/sleep', name: 'sleep', label: 'Sleep & recovery', icon: 'moon' }] : []),
    ],
  },
  { label: 'Training', items: [{ to: '/training', name: 'training', label: 'Training', icon: 'dumbbell' }] },
  {
    label: 'Explore',
    items: [
      { to: '/day', name: 'day', label: 'Day view', icon: 'calendar' },
      { to: '/insights', name: 'insights', label: 'Insights', icon: 'bulb' },
    ],
  },
  {
    label: 'Data',
    items: [
      { to: '/import', name: 'import', label: 'Import', icon: 'upload' },
      { to: '/assistant', name: 'assistant', label: 'AI assistants', icon: 'bot' },
      { to: '/settings', name: 'settings', label: 'Settings', icon: 'settings' },
    ],
  },
])

const tabs: Item[] = [
  { to: '/', name: 'dashboard', label: 'Dashboard', icon: 'home' },
  { to: '/activity', name: 'activity', label: 'Activity', icon: 'footprints' },
  { to: '/heart', name: 'heart', label: 'Heart', icon: 'heart' },
  { to: '/training', name: 'training', label: 'Training', icon: 'dumbbell' },
]
const moreNames = ['more', 'day', 'sleep', 'insights', 'import', 'assistant', 'settings']

function active(name: string) {
  if (name === 'training') return route.name === 'training' || route.name === 'workout'
  if (name === 'settings') return String(route.name).startsWith('settings')
  return route.name === name
}

const title = computed(() => pageMeta.value.title ?? route.meta.title ?? 'Ritm')
</script>

<template>
  <div class="shell">
    <nav class="sidebar" aria-label="Main">
      <RouterLink to="/" class="brand">
        <span class="logo" aria-hidden="true"><Icon name="activity" :size="18" /></span>
        <span>Ritm</span>
      </RouterLink>

      <div class="groups">
        <div v-for="(g, i) in groups" :key="i" class="group">
          <div v-if="g.label" class="group-label">{{ g.label }}</div>
          <RouterLink v-for="item in g.items" :key="item.to" :to="item.to" class="side-link" :class="{ active: active(item.name) }"
            :aria-current="active(item.name) ? 'page' : undefined">
            <Icon :name="item.icon" :size="18" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </div>
      </div>

      <RouterLink to="/import" class="fresh" :class="{ stale: fresh.stale.value }">
        <span class="dot" aria-hidden="true" />
        <span class="fresh-text">
          <strong>{{ fresh.label.value }}</strong>
          <span>{{ fresh.stale.value ? 'Time for a new export' : 'Import a newer export' }}</span>
        </span>
        <Icon name="upload" :size="16" />
      </RouterLink>

      <div class="user">
        <span class="avatar" aria-hidden="true">{{ (user?.username ?? '?').slice(0, 1).toUpperCase() }}</span>
        <span class="user-name">
          <strong>{{ user?.username }}</strong>
          <span>Ritm {{ version }}</span>
        </span>
        <button class="btn ghost icon-btn small" type="button" aria-label="Sign out" title="Sign out" @click="logout">
          <Icon name="logout" :size="18" />
        </button>
      </div>
    </nav>

    <div class="main">
      <header class="topbar">
        <div class="topbar-inner" :class="{ left: route.meta.left }">
          <div class="heading">
            <RouterLink v-if="pageMeta.back" :to="pageMeta.back.to" class="back small">
              <Icon name="left" :size="14" /> {{ pageMeta.back.label }}
            </RouterLink>
            <h1>{{ title }}</h1>
            <p v-if="pageMeta.subtitle" class="subtitle">{{ pageMeta.subtitle }}</p>
          </div>
          <div class="actions">
            <RouterLink v-if="route.meta.freshness" to="/import" class="chip fresh-chip" :class="fresh.stale.value ? 'attention' : 'accent'">
              <Icon :name="fresh.stale.value ? 'alert' : 'clock'" :size="13" />
              {{ fresh.label.value }}
            </RouterLink>
            <div id="topbar-actions" class="slot" />
            <PeriodPicker v-if="route.meta.period" :compare="!!route.meta.compare" />
          </div>
        </div>
      </header>
      <main class="content">
        <slot />
      </main>
    </div>

    <nav class="tabbar" aria-label="Main">
      <RouterLink v-for="item in tabs" :key="item.to" :to="item.to" class="tab" :class="{ active: active(item.name) }">
        <Icon :name="item.icon" :size="22" />
        <span>{{ item.label }}</span>
      </RouterLink>
      <RouterLink to="/more" class="tab" :class="{ active: moreNames.includes(String(route.name)) || active('settings') }">
        <Icon name="more" :size="22" />
        <span>More</span>
      </RouterLink>
    </nav>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100dvh;
}
.main {
  min-width: 0;
}
.content {
  padding-bottom: calc(var(--tabbar-h) + var(--safe-bottom) + 8px);
  min-width: 0;
}
/* the page's own top padding is replaced by the top bar */
.content > :deep(.page) {
  padding-top: 4px;
}

/* ---- top bar ---- */
.topbar {
  padding: calc(14px + var(--safe-top)) calc(16px + var(--safe-right)) 12px calc(16px + var(--safe-left));
}
.topbar-inner {
  max-width: 1280px;
  margin: 0 auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 16px;
}
.topbar-inner.left {
  margin-left: 0;
}
.heading {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.heading h1 {
  font-size: 1.45rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.subtitle {
  font-size: 0.85rem;
  color: var(--muted);
}
.back {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--text-2);
  margin-bottom: 2px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 10px;
  min-width: 0;
}
.slot {
  display: contents;
}
.fresh-chip {
  min-height: 30px;
}

/* ---- phone tab bar ---- */
.sidebar {
  display: none;
}
.tabbar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 20;
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  height: calc(var(--tabbar-h) + var(--safe-bottom));
  padding: 0 var(--safe-right) var(--safe-bottom) var(--safe-left);
  background: rgba(22, 25, 32, 0.96);
  backdrop-filter: blur(12px);
  border-top: 1px solid var(--border);
}
.tab {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-height: var(--tap);
  color: var(--muted);
  font-size: 0.7rem;
  font-weight: 560;
  text-decoration: none;
  -webkit-tap-highlight-color: transparent;
}
.tab.active {
  color: var(--accent);
}

@media (min-width: 640px) {
  .topbar {
    position: sticky;
    top: 0;
    z-index: 15;
    padding: 16px 24px 12px;
    background: rgba(15, 17, 21, 0.86);
    backdrop-filter: blur(14px);
    -webkit-backdrop-filter: blur(14px);
    border-bottom: 1px solid var(--border);
  }
  .content > :deep(.page) {
    padding-top: 20px;
  }
}

/* ---- desktop sidebar ---- */
@media (min-width: 1024px) {
  .shell {
    display: grid;
    grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
  }
  .tabbar {
    display: none;
  }
  .content {
    padding-bottom: 0;
  }
  .topbar {
    padding: 16px 32px 12px;
  }
  .fresh-chip {
    display: none; /* the sidebar shows freshness on desktop */
  }
  .sidebar {
    position: sticky;
    top: 0;
    height: 100dvh;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 18px 12px 14px;
    background: var(--surface);
    border-right: 1px solid var(--border);
    overflow-y: auto;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 2px 10px 6px;
    font-weight: 700;
    font-size: 1.15rem;
    color: var(--text);
    text-decoration: none;
  }
  .logo {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    border-radius: 9px;
    background: linear-gradient(135deg, var(--accent-bg), rgba(122, 162, 255, 0.28));
    color: var(--accent);
  }
  .groups {
    display: flex;
    flex-direction: column;
    gap: 14px;
    flex: 1;
  }
  .group {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .group-label {
    padding: 0 12px 4px;
    font-size: 0.68rem;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .side-link {
    position: relative;
    display: flex;
    align-items: center;
    gap: 12px;
    min-height: 40px;
    padding: 0 12px;
    border-radius: var(--radius-sm);
    color: var(--text-2);
    font-weight: 540;
    font-size: 0.92rem;
    text-decoration: none;
  }
  .side-link:hover {
    background: var(--elevated);
    color: var(--text);
  }
  .side-link.active {
    background: var(--accent-bg);
    color: var(--text);
  }
  .side-link.active .icon {
    color: var(--accent);
  }
  .side-link.active::before {
    content: '';
    position: absolute;
    left: -12px;
    top: 8px;
    bottom: 8px;
    width: 3px;
    border-radius: 0 3px 3px 0;
    background: var(--accent);
  }
  .fresh {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text-2);
    text-decoration: none;
  }
  .fresh:hover {
    border-color: var(--border-strong);
    text-decoration: none;
  }
  .fresh .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--positive);
    box-shadow: 0 0 0 3px var(--positive-bg);
  }
  .fresh.stale .dot {
    background: var(--attention);
    box-shadow: 0 0 0 3px var(--attention-bg);
  }
  .fresh-text {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    font-size: 0.75rem;
    line-height: 1.3;
  }
  .fresh-text strong {
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text);
  }
  .user {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 4px 0 8px;
    border-top: 1px solid var(--border);
    padding-top: 12px;
  }
  .avatar {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 50%;
    background: var(--elevated);
    border: 1px solid var(--border-strong);
    font-weight: 650;
    font-size: 0.85rem;
  }
  .user-name {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    font-size: 0.75rem;
    color: var(--muted);
    line-height: 1.3;
  }
  .user-name strong {
    font-size: 0.85rem;
    color: var(--text);
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}
</style>
