<script setup lang="ts">
// Set-up guide for one kind of client: generate a key for it, then the steps with that key filled in.
import { computed, ref } from 'vue'
import Icon from '../../components/Icon.vue'
import Modal from '../../components/Modal.vue'
import { useCopy } from '../../composables/useKeys'
import { API_BASE } from '../../lib/api'
import type { KeyScope } from '../../lib/types'
import { CLIENTS } from './clients'

const props = defineProps<{ scope: KeyScope; base: string; secret: string | null; busy: boolean; active: number }>()
const emit = defineEmits<{ close: []; generate: [name: string] }>()
const copy = useCopy()

const client = computed(() => CLIENTS[props.scope])
const name = ref(client.value.defaultName)
const token = computed(() => props.secret ?? 'ritm_…')
const uploadURL = computed(() => `${props.base}${API_BASE}/imports`)
const freshnessURL = computed(() => `${props.base}${API_BASE}/monitor/freshness`)
const healthURL = computed(() => `${props.base}/healthz`)
const mcpConfig = computed(() => JSON.stringify({ mcpServers: { ritm: { type: 'http', url: `${props.base}/mcp`, headers: { Authorization: `Bearer ${token.value}` } } } }, null, 2))
</script>

<template>
  <Modal :title="`Connect ${client.label}`" @close="emit('close')">
    <section class="step">
      <h3><span class="num">1</span> Generate a key</h3>
      <template v-if="!secret">
        <p class="small text-2">
          {{ client.keyNote }}
          <template v-if="active"> You already have {{ active }} active — reuse it if you still have the secret.</template>
        </p>
        <form class="row" @submit.prevent="emit('generate', name.trim())">
          <input v-model="name" class="input grow" placeholder="Key name" aria-label="Key name" required maxlength="80" />
          <button class="btn primary" type="submit" :disabled="busy || !name.trim()"><Icon name="key" :size="16" /> Generate</button>
        </form>
      </template>
      <template v-else>
        <p class="small text-2">Copy this key now — it will not be shown again. It is already filled in below.</p>
        <div class="copyline secret">
          <code>{{ secret }}</code>
          <button class="btn small icon-btn" type="button" aria-label="Copy key" @click="copy(secret)"><Icon name="copy" :size="16" /></button>
        </div>
      </template>
    </section>

    <section v-if="scope === 'ingest'" class="step">
      <h3><span class="num">2</span> Create the shortcut</h3>
      <ol class="steps small text-2">
        <li>In Shortcuts, create a shortcut that shows in the Share Sheet and receives <strong>Files</strong>.</li>
        <li>Add <strong>Get Contents of URL</strong> with method <strong>POST</strong> and request body <strong>File</strong> = Shortcut Input.</li>
        <li>Add the headers below.</li>
        <li>After exporting in Health, share <code>export.zip</code> to the shortcut. Progress shows on the <RouterLink to="/import" @click="emit('close')">Import</RouterLink> page.</li>
      </ol>
      <div class="copyline">
        <code>POST {{ uploadURL }}</code>
        <button class="btn small icon-btn" type="button" aria-label="Copy upload URL" @click="copy(uploadURL)"><Icon name="copy" :size="16" /></button>
      </div>
      <pre>Authorization: Bearer {{ token }}
X-File-Name: export.zip</pre>
      <details>
        <summary class="small">Or upload with curl</summary>
        <pre>curl -H "Authorization: Bearer {{ token }}" -H "X-File-Name: export.zip" \
  --data-binary @export.zip {{ uploadURL }}</pre>
      </details>
      <p class="muted small">An ingest key can only upload exports and read their import status. The API is described at
        <a :href="`${API_BASE}/docs`" target="_blank" rel="noopener">{{ API_BASE }}/docs</a>.</p>
    </section>

    <section v-else-if="scope === 'monitor'" class="step">
      <h3><span class="num">2</span> Add two HTTP(s) monitors in Uptime Kuma</h3>
      <div class="monitor">
        <strong class="small">Ritm — data freshness</strong>
        <div class="copyline">
          <code>{{ freshnessURL }}</code>
          <button class="btn small icon-btn" type="button" aria-label="Copy freshness URL" @click="copy(freshnessURL)"><Icon name="copy" :size="16" /></button>
        </div>
        <pre>{"Authorization": "Bearer {{ token }}"}</pre>
        <p class="muted small">Headers as above, interval 1 h, retries 0, attach a notification (e.g. Telegram).</p>
      </div>
      <div class="monitor">
        <strong class="small">Ritm — up</strong>
        <div class="copyline">
          <code>{{ healthURL }}</code>
          <button class="btn small icon-btn" type="button" aria-label="Copy health URL" @click="copy(healthURL)"><Icon name="copy" :size="16" /></button>
        </div>
        <p class="muted small">No auth. Interval 1–5 min, retries 2.</p>
      </div>
      <details>
        <summary class="small">How the freshness check works</summary>
        <p class="muted small">
          It returns <code>200</code> while data is fresh and <code>503</code> once nothing new arrived for the stale threshold under
          <RouterLink :to="{ name: 'settings-sources' }" @click="emit('close')">Sources</RouterLink>, so Kuma's default accepted codes (200–299) work as is.
          Add <code>?max_age=10d</code> to the URL for a different threshold per monitor. For stricter checking use the
          <em>HTTP(s) – Json Query</em> type with expression <code>status</code> and expected value <code>ok</code>.
        </p>
      </details>
    </section>

    <section v-else class="step">
      <h3><span class="num">2</span> Add Ritm to your assistant</h3>
      <p class="small text-2">MCP client config (streamable HTTP):</p>
      <pre>{{ mcpConfig }}</pre>
      <p class="muted small">Ready-made snippets for Claude Code, Claude Desktop, VS Code and Cursor are on
        <RouterLink to="/assistant" @click="emit('close')">AI assistants</RouterLink>.</p>
    </section>

    <div class="row end">
      <button class="btn" type="button" @click="emit('close')">Done</button>
    </div>
  </Modal>
</template>

<style scoped>
.step {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.step h3 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 0.95rem;
}
.step p {
  margin: 0;
}
.num {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--accent-bg);
  color: var(--accent);
  font-size: 0.75rem;
}
.grow {
  flex: 1 1 200px;
}
.steps {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.copyline {
  display: flex;
  align-items: center;
  gap: 6px;
}
.copyline code {
  overflow-wrap: anywhere;
  min-width: 0;
}
.secret code {
  font-size: 0.9rem;
}
.monitor {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  border-radius: var(--radius-sm);
  background: var(--surface);
}
pre {
  margin: 0;
}
.end {
  justify-content: flex-end;
}
</style>
