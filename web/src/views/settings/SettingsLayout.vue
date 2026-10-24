<script setup lang="ts">
// Settings shell: a section menu (side list on desktop, scrolling tabs on phones) and the current section.
import { nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import Icon from '../../components/Icon.vue'
import StateBlock from '../../components/StateBlock.vue'
import { useAsync } from '../../composables/useAsync'
import { useSettings } from '../../composables/useSettings'

const { data, load } = useSettings()
const state = useAsync(() => load(true))

const sections = [
  { name: 'settings-general', label: 'General', hint: 'Goals, day and time zone', icon: 'settings' },
  { name: 'settings-heart', label: 'Heart & training', hint: 'Zones and training load', icon: 'heart' },
  { name: 'settings-sources', label: 'Sources', hint: 'Device priority and freshness', icon: 'layers' },
  { name: 'settings-connections', label: 'Connections', hint: 'iPhone, Uptime Kuma, AI assistants', icon: 'link' },
  { name: 'settings-data', label: 'Data', hint: 'Coverage and reset', icon: 'database' },
  { name: 'settings-account', label: 'Account', hint: 'Password and deletion', icon: 'user' },
]

// On phones the tabs scroll sideways; keep the current one in view.
const route = useRoute()
const nav = ref<HTMLElement | null>(null)
watch(
  () => route.name,
  () => nextTick(() => nav.value?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })),
)
onMounted(() => nav.value?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' }))
</script>

<template>
  <div class="page settings">
    <nav ref="nav" class="snav" aria-label="Settings sections">
      <RouterLink v-for="s in sections" :key="s.name" :to="{ name: s.name }" class="slink" active-class="active">
        <Icon :name="s.icon" :size="18" />
        <span class="stext">
          <span>{{ s.label }}</span>
          <span class="shint">{{ s.hint }}</span>
        </span>
      </RouterLink>
    </nav>
    <div class="sbody">
      <StateBlock :loading="state.loading.value" :error="state.error.value" :empty="!data" :height="400">
        <RouterView />
      </StateBlock>
    </div>
  </div>
</template>

<style scoped>
.settings {
  gap: 12px;
  margin-left: 0;
}
.snav {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  scrollbar-width: none;
  margin: 0 calc(-16px - var(--safe-right)) 0 calc(-16px - var(--safe-left));
  padding: 0 16px 2px;
}
.slink {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  min-height: 38px;
  padding: 0 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-2);
  font-size: 0.88rem;
  font-weight: 540;
  text-decoration: none;
  white-space: nowrap;
}
.slink:hover {
  color: var(--text);
  text-decoration: none;
}
.slink.active {
  background: var(--accent-bg);
  border-color: transparent;
  color: var(--text);
}
.slink.active .icon {
  color: var(--accent);
}
.slink .icon {
  display: none;
}
.shint {
  display: none;
}
.sbody {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

@media (min-width: 1200px) {
  .settings {
    display: grid;
    grid-template-columns: 240px minmax(0, 1fr);
    align-items: start;
    gap: 24px;
  }
  .snav {
    position: sticky;
    top: 96px;
    flex-direction: column;
    gap: 2px;
    margin: 0;
    padding: 0;
    overflow: visible;
  }
  .slink {
    align-items: flex-start;
    gap: 12px;
    padding: 9px 12px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    white-space: normal;
  }
  .slink:hover {
    background: var(--elevated);
  }
  .slink .icon {
    display: block;
    margin-top: 1px;
  }
  .stext {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .shint {
    display: block;
    font-size: 0.75rem;
    font-weight: 400;
    color: var(--muted);
  }
}
</style>

<style>
/* shared by the settings sections */
.settings .sform {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.settings .form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
.settings .card p {
  margin: 0;
}
.settings .hint {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.settings .copyline {
  display: flex;
  align-items: center;
  gap: 6px;
}
.settings .copyline code {
  overflow-wrap: anywhere;
  min-width: 0;
}
.settings .danger-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.settings .danger-row p {
  margin: 2px 0 0;
}
</style>
