<script setup lang="ts">
import { computed, ref } from 'vue'
import Card from '../../components/Card.vue'
import Icon from '../../components/Icon.vue'
import StateBlock from '../../components/StateBlock.vue'
import { useFreshness } from '../../composables/useFreshness'
import { useKeys } from '../../composables/useKeys'
import { useSettingsDraft } from '../../composables/useSettingsDraft'
import { ago } from '../../lib/fmt'
import type { ApiKey, KeyScope } from '../../lib/types'
import { CLIENTS } from './clients'
import ConnectionGuide from './ConnectionGuide.vue'
import SaveBar from './SaveBar.vue'

const { form, server, dirty, saving, submit, discard } = useSettingsDraft()
const { keys, created, creating, create, revoke } = useKeys()
const fresh = useFreshness()
fresh.load(true)
const baseURL = computed(() => String(server.value?.base_url || window.location.origin))

const scopes = Object.keys(CLIENTS) as KeyScope[]
const all = computed(() => keys.data.value?.items ?? [])
const active = computed(() => all.value.filter((k) => !k.revoked_at))
const revoked = computed(() => all.value.filter((k) => k.revoked_at))
const showRevoked = ref(false)
const activeFor = (s: KeyScope) => active.value.filter((k) => k.scope === s)
function lastUsed(s: KeyScope) {
  const t = activeFor(s).map((k) => k.last_used_at).filter(Boolean).sort().at(-1)
  return t ? `used ${ago(t)}` : 'not used yet'
}

const kuma = computed(() => {
  const f = fresh.data.value
  if (!f) return null
  const code = !f.last_data_at ? '503 no_data' : f.stale ? '503 stale' : '200 ok'
  return { code, ok: code === '200 ok', checked: f.last_checked_at ? `checked ${ago(f.last_checked_at)}` : 'never checked' }
})

// The guide for one client; generating a key there fills the key into its instructions.
const guide = ref<KeyScope | null>(null)
function openGuide(s: KeyScope) {
  created.value = null
  guide.value = s
}
async function generate(name: string) {
  if (guide.value) await create(name, guide.value)
}
function closeGuide() {
  guide.value = null
  created.value = null
}
const label = (k: ApiKey) => CLIENTS[k.scope]?.label ?? k.scope
</script>

<template>
  <form v-if="form" class="sform" @submit.prevent="submit">
    <div class="clients">
      <article v-for="s in scopes" :key="s" class="client">
        <div class="chead">
          <span class="cicon"><Icon :name="CLIENTS[s].icon" :size="18" /></span>
          <strong>{{ CLIENTS[s].label }}</strong>
        </div>
        <p class="muted small">{{ CLIENTS[s].blurb }}</p>
        <div class="cstatus small">
          <template v-if="activeFor(s).length">
            <span class="chip positive"><Icon name="check" :size="12" /> {{ activeFor(s).length }} key{{ activeFor(s).length > 1 ? 's' : '' }}</span>
            <span class="muted">{{ s === 'monitor' && kuma ? kuma.checked : lastUsed(s) }}</span>
          </template>
          <span v-else class="chip">Not connected</span>
          <span v-if="s === 'monitor' && kuma && activeFor(s).length" class="chip" :class="kuma.ok ? 'positive' : 'attention'">{{ kuma.code }}</span>
        </div>
        <label v-if="s === 'read'" class="check small">
          <input v-model="form.allow_sql" type="checkbox" />
          <span>Allow read-only SQL queries over MCP (<code>query_sql</code>) for every read key</span>
        </label>
        <button class="btn small" :class="{ primary: !activeFor(s).length }" type="button" @click="openGuide(s)">
          {{ !activeFor(s).length ? 'Set up' : s === 'ingest' ? 'Connect an iPhone' : 'Add key / show setup' }}
        </button>
      </article>
    </div>

    <Card title="API keys" subtitle="Each key belongs to one client. The secret is shown only when it is created.">
      <StateBlock :loading="keys.loading.value" :error="keys.error.value" :empty="!active.length" empty-text="No active keys — set up a client above.">
        <ul class="keys">
          <li v-for="k in active" :key="k.id">
            <span class="cicon small-icon"><Icon :name="CLIENTS[k.scope]?.icon ?? 'key'" :size="16" /></span>
            <div class="kinfo">
              <strong>{{ k.name }}</strong> <span class="muted small">· {{ label(k) }}</span>
              <div class="muted small"><code>{{ k.prefix }}…</code> · created {{ ago(k.created_at) }} · last used {{ ago(k.last_used_at) }}</div>
            </div>
            <button class="btn small ghost" type="button" @click="openGuide(k.scope)">Setup</button>
            <button class="btn small danger" type="button" @click="revoke(k)"><Icon name="trash" :size="16" /> Revoke</button>
          </li>
        </ul>
      </StateBlock>
      <template v-if="revoked.length">
        <button class="btn small ghost toggle" type="button" @click="showRevoked = !showRevoked">
          {{ showRevoked ? 'Hide' : 'Show' }} {{ revoked.length }} revoked
        </button>
        <ul v-if="showRevoked" class="keys revoked">
          <li v-for="k in revoked" :key="k.id">
            <span class="cicon small-icon"><Icon :name="CLIENTS[k.scope]?.icon ?? 'key'" :size="16" /></span>
            <div class="kinfo">
              <strong>{{ k.name }}</strong> <span class="muted small">· {{ label(k) }}</span>
              <div class="muted small"><code>{{ k.prefix }}…</code> · created {{ ago(k.created_at) }} · revoked {{ ago(k.revoked_at) }}</div>
            </div>
          </li>
        </ul>
      </template>
    </Card>

    <SaveBar :dirty="dirty" :saving="saving" @discard="discard" />

    <ConnectionGuide v-if="guide" :scope="guide" :base="baseURL" :shortcut-url="server?.shortcut_url ?? ''" :secret="created?.secret ?? null" :busy="creating"
      :active="activeFor(guide).length" @generate="generate" @close="closeGuide" />
  </form>
</template>

<style scoped>
.clients {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--gap);
}
.client {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.client > .btn {
  margin-top: auto;
  align-self: flex-start;
}
.chead {
  display: flex;
  align-items: center;
  gap: 10px;
}
.cicon {
  display: grid;
  place-items: center;
  flex: none;
  width: 34px;
  height: 34px;
  border-radius: 9px;
  background: var(--accent-bg);
  color: var(--accent);
}
.cicon.small-icon {
  width: 30px;
  height: 30px;
  background: var(--elevated);
  color: var(--text-2);
}
.cstatus {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.check {
  min-height: 0;
}
.keys {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.keys li {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 10px 0;
  border-top: 1px solid var(--border);
}
.keys li:first-child {
  border-top: 0;
  padding-top: 0;
}
.kinfo {
  flex: 1 1 220px;
  min-width: 0;
}
.revoked li {
  opacity: 0.55;
}
.toggle {
  align-self: flex-start;
}
</style>
