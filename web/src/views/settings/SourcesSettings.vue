<script setup lang="ts">
import { computed, ref } from 'vue'
import Card from '../../components/Card.vue'
import Icon from '../../components/Icon.vue'
import StateBlock from '../../components/StateBlock.vue'
import { useFreshness } from '../../composables/useFreshness'
import { useSettingsDraft } from '../../composables/useSettingsDraft'
import { ago } from '../../lib/fmt'
import SaveBar from './SaveBar.vue'

const { form, res, server, dirty, saving, submit, discard } = useSettingsDraft()
const fresh = useFreshness()
fresh.load(true)

// Sources in draft order; ones missing from the saved list keep the server's order after the rest.
const sources = computed(() => {
  const list = res.value?.sources ?? []
  const pri = form.value?.source_priority ?? []
  const rank = (name: string, i: number) => (pri.includes(name) ? pri.indexOf(name) : pri.length + i)
  return list.map((s, i) => ({ s, r: rank(s.name, i) })).sort((a, b) => a.r - b.r).map((x) => x.s)
})
function move(from: number, to: number) {
  if (!form.value || to < 0 || to >= sources.value.length || from === to) return
  const list = sources.value.map((s) => s.name)
  list.splice(to, 0, ...list.splice(from, 1))
  form.value.source_priority = list
}
const dragFrom = ref<number | null>(null)
const dragOver = ref<number | null>(null)
function drop(i: number) {
  if (dragFrom.value !== null) move(dragFrom.value, i)
  dragFrom.value = dragOver.value = null
}

const staleDays = computed({
  get: () => (form.value?.stale_after_hours ?? Number(server.value?.stale_after_hours ?? 336)) / 24,
  set: (v: number) => form.value && (form.value.stale_after_hours = v > 0 ? v * 24 : null),
})
const state = computed(() => {
  const f = fresh.data.value
  if (!f) return null
  if (!f.last_data_at) return { label: 'No data yet', cls: 'attention', icon: 'alert' }
  if (f.stale) return { label: 'Stale', cls: 'attention', icon: 'alert' }
  return { label: 'Fresh', cls: 'positive', icon: 'check' }
})
</script>

<template>
  <form v-if="form && res" class="sform" @submit.prevent="submit">
    <Card title="Source priority" subtitle="When several devices record the same data, the highest one wins (avoids double counting): per hour for steps, distance, flights and energy; per day for heart metrics; per night for sleep. Drag or use the arrows to reorder.">
      <StateBlock :empty="!sources.length" empty-text="Sources appear after the first import.">
        <ol class="sources">
          <li v-for="(s, i) in sources" :key="s.name" draggable="true" :class="{ over: dragOver === i && dragFrom !== i, dragging: dragFrom === i }"
            @dragstart="dragFrom = i" @dragover.prevent="dragOver = i" @dragend="dragFrom = dragOver = null" @drop.prevent="drop(i)">
            <Icon name="grip" :size="16" class="grip" />
            <span class="tnum muted">{{ i + 1 }}</span>
            <span class="sname">{{ s.name }}</span>
            <span class="chip">{{ s.kind }}</span>
            <span class="spacer" />
            <button class="btn small ghost icon-btn" type="button" :aria-label="`Move ${s.name} up`" :disabled="i === 0" @click="move(i, i - 1)">↑</button>
            <button class="btn small ghost icon-btn" type="button" :aria-label="`Move ${s.name} down`" :disabled="i === sources.length - 1" @click="move(i, i + 1)">↓</button>
          </li>
        </ol>
      </StateBlock>
    </Card>

    <Card title="Data freshness" subtitle="When Ritm reminds you to import a newer export, and when Uptime Kuma alerts">
      <template #actions>
        <span v-if="state" class="chip" :class="state.cls"><Icon :name="state.icon" :size="13" /> {{ state.label }}</span>
      </template>
      <div class="form-grid">
        <label class="field"><span>Stale after (days without new data)</span>
          <input v-model.number="staleDays" class="input" type="number" min="1" max="90" inputmode="numeric" /></label>
        <div class="field"><span>Last data</span><div class="value">{{ ago(fresh.data.value?.last_data_at) }}</div></div>
      </div>
      <p class="muted small">To get alerted when data goes stale, set up <RouterLink :to="{ name: 'settings-connections' }">Uptime Kuma under Connections</RouterLink>.</p>
    </Card>

    <SaveBar :dirty="dirty" :saving="saving" @discard="discard" />
  </form>
</template>

<style scoped>
.sources {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.sources li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 2px 4px 2px 8px;
  background: var(--elevated);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  cursor: grab;
}
.sources li.over {
  border-color: var(--accent);
}
.sources li.dragging {
  opacity: 0.5;
}
.sources .btn {
  min-height: 34px;
  min-width: 34px;
}
.grip {
  color: var(--muted);
}
.sname {
  overflow-wrap: anywhere;
}
.value {
  min-height: var(--tap);
  display: flex;
  align-items: center;
}
</style>
