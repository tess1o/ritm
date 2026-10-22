<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Card from '../components/Card.vue'
import Icon from '../components/Icon.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { useCopy, useKeys } from '../composables/useKeys'
import { get } from '../lib/api'
import { ago } from '../lib/fmt'
import type { McpInfo } from '../lib/types'

const info = useAsync((s) => get<McpInfo>('/mcp/catalog', s))
const { keys, created, creating, create } = useKeys()
const copy = useCopy()

const endpoint = computed(() => `${info.data.value?.base_url || window.location.origin}${info.data.value?.path ?? '/mcp'}`)
const usingOrigin = computed(() => info.data.value && !info.data.value.base_url)

// Key: an existing mcp key (its secret is not stored, so snippets show a placeholder) or a new one.
const mcpKeys = computed(() => (keys.data.value?.items ?? []).filter((k) => k.scope === 'mcp' && !k.revoked_at))
const choice = ref<number | 'new'>('new')
watch(mcpKeys, (list) => {
  if (choice.value === 'new' && list.length && !created.value) choice.value = list[0].id
})
const newName = ref('Claude')
async function createKey() {
  const k = await create(newName.value, 'mcp')
  if (k) choice.value = k.key.id
}
const selected = computed(() => mcpKeys.value.find((k) => k.id === choice.value))
const secret = computed(() => (created.value && created.value.key.id === choice.value ? created.value.secret : null))
const keyText = computed(() => secret.value ?? 'ritm_…')

// Client setup snippets.
type Client = { id: string; label: string; where: string; code: string; note?: string }
const clients = computed<Client[]>(() => {
  const url = endpoint.value
  const auth = `Bearer ${keyText.value}`
  const insecure = url.startsWith('http://') && !/^http:\/\/(localhost|127\.0\.0\.1)[:/]/.test(url)
  return [
    {
      id: 'claude-code', label: 'Claude Code',
      where: 'Run in a terminal. Add --scope user to use Ritm in every project.',
      code: `claude mcp add --transport http ritm ${url} \\\n  --header "Authorization: ${auth}"`,
    },
    {
      id: 'claude-desktop', label: 'Claude Desktop',
      where: 'Settings → Developer → Edit Config, add this to claude_desktop_config.json and restart Claude. Needs Node.js (mcp-remote bridges HTTP to the desktop app).',
      code: JSON.stringify({
        mcpServers: {
          ritm: {
            command: 'npx',
            args: ['-y', 'mcp-remote', url, '--header', 'Authorization:${RITM_AUTH}', ...(insecure ? ['--allow-http'] : [])],
            env: { RITM_AUTH: auth },
          },
        },
      }, null, 2),
    },
    {
      id: 'vscode', label: 'VS Code',
      where: '.vscode/mcp.json in a workspace, or “MCP: Open User Configuration” for all workspaces.',
      code: JSON.stringify({ servers: { ritm: { type: 'http', url, headers: { Authorization: auth } } } }, null, 2),
    },
    {
      id: 'cursor', label: 'Cursor',
      where: '~/.cursor/mcp.json (or .cursor/mcp.json in a project).',
      code: JSON.stringify({ mcpServers: { ritm: { url, headers: { Authorization: auth } } } }, null, 2),
    },
    {
      id: 'other', label: 'Other',
      where: 'Any MCP client that supports the streamable HTTP transport with a custom header.',
      code: `URL:     ${url}\nHeader:  Authorization: ${auth}`,
      note: 'Claude.ai web and mobile custom connectors need OAuth sign-in, which Ritm does not offer; use a desktop or CLI client.',
    },
  ]
})
const tab = ref('claude-code')
const client = computed(() => clients.value.find((c) => c.id === tab.value)!)

const examples = [
  'How was my last month compared with the month before?',
  'Is my resting heart rate improving? Show the trend for the last 6 months.',
  'Am I keeping up 3 gym sessions a week? How is my training load?',
  'How do my workouts affect next-day resting HR and HRV?',
  'Which weekdays am I least active, and at what hours do I sit the longest?',
]

</script>

<template>
  <div class="page">
    <Card title="Connect an AI assistant" subtitle="Ask Claude or another assistant about your data in plain language">
      <p class="text-2">
        Ritm runs a read-only <a href="https://modelcontextprotocol.io" target="_blank" rel="noopener">MCP</a> server. An assistant connected
        with your key can look up your activity, heart, sleep and training data and the insights, but cannot change anything.
        Whatever it reads becomes part of your conversation with that assistant's provider.
      </p>
      <div class="endpoint">
        <span class="muted small">Endpoint</span>
        <div class="row nowrap">
          <code>{{ endpoint }}</code>
          <button class="btn small icon-btn" type="button" aria-label="Copy endpoint URL" @click="copy(endpoint)"><Icon name="copy" :size="16" /></button>
        </div>
        <p v-if="usingOrigin" class="muted small">Using this page's address. If assistants reach Ritm under another URL, set <code>RITM_BASE_URL</code>.</p>
      </div>
    </Card>

    <Card title="1 · Choose an API key" subtitle="Keys with the mcp scope are read-only and can be revoked any time">
      <StateBlock :loading="keys.loading.value" :error="keys.error.value">
        <div class="keys" role="radiogroup" aria-label="API key">
          <label v-for="k in mcpKeys" :key="k.id" class="opt" :class="{ on: choice === k.id }">
            <input v-model="choice" type="radio" name="key" :value="k.id" />
            <span class="grow">
              <strong>{{ k.name }}</strong> <code class="small">{{ k.prefix }}…</code>
              <span class="muted small block">created {{ ago(k.created_at) }} · last used {{ ago(k.last_used_at) }}</span>
            </span>
          </label>
          <label class="opt" :class="{ on: choice === 'new' }">
            <input v-model="choice" type="radio" name="key" value="new" />
            <span class="grow">
              <strong>Create a new key</strong>
              <span class="muted small block">Recommended: one key per assistant, so you can revoke them separately.</span>
            </span>
          </label>
        </div>

        <form v-if="choice === 'new'" class="row create" @submit.prevent="createKey">
          <input v-model="newName" class="input grow" placeholder="Key name, e.g. Claude Desktop" required maxlength="80" aria-label="Key name" />
          <button class="btn primary" type="submit" :disabled="creating"><Icon name="key" /> {{ creating ? 'Creating…' : 'Create key' }}</button>
        </form>

        <div v-if="secret" class="secret" role="status">
          <p class="small"><Icon name="alert" :size="14" /> Copy this key now. It is shown only once; the snippets below already include it.</p>
          <div class="row nowrap">
            <code>{{ secret }}</code>
            <button class="btn small icon-btn" type="button" aria-label="Copy key" @click="copy(secret!, 'Key copied')"><Icon name="copy" :size="16" /></button>
          </div>
        </div>
        <p v-else-if="selected" class="muted small">
          Ritm stores only a hash of each key, so it cannot show <strong>{{ selected.name }}</strong> again. Replace <code>ritm_…</code> below
          with the key you saved, or create a new one if it is lost.
        </p>
      </StateBlock>
    </Card>

    <Card title="2 · Add Ritm to your assistant">
      <div class="tabs" role="tablist" aria-label="Client">
        <button v-for="c in clients" :key="c.id" type="button" role="tab" :aria-selected="tab === c.id" :class="{ on: tab === c.id }"
          @click="tab = c.id">{{ c.label }}</button>
      </div>
      <p class="small text-2">{{ client.where }}</p>
      <div class="snippet">
        <pre>{{ client.code }}</pre>
        <button class="btn small copybtn" type="button" @click="copy(client.code)"><Icon name="copy" :size="16" /> Copy</button>
      </div>
      <p v-if="client.note" class="muted small">{{ client.note }}</p>
    </Card>

    <Card title="3 · Ask something">
      <ul class="examples">
        <li v-for="q in examples" :key="q">
          <span>“{{ q }}”</span>
          <button class="btn small ghost icon-btn" type="button" :aria-label="`Copy: ${q}`" @click="copy(q)"><Icon name="copy" :size="16" /></button>
        </li>
      </ul>
      <p v-if="info.data.value?.catalog.prompts.length" class="small text-2">
        Ready-made prompts (in Claude Code type <code>/</code>):
        <template v-for="(p, i) in info.data.value.catalog.prompts" :key="p.name">
          <code>{{ p.name }}</code><template v-if="i < info.data.value.catalog.prompts.length - 1">, </template>
        </template>.
      </p>
    </Card>

    <Card title="What the assistant can use" subtitle="All tools are read-only. Dates accept YYYY-MM-DD or names such as last_30d, last_month or ytd.">
      <StateBlock :loading="info.loading.value" :error="info.error.value" :empty="!info.data.value?.catalog.tools.length">
        <div v-if="info.data.value" class="tools">
          <details v-for="t in info.data.value.catalog.tools" :key="t.name" class="tool">
            <summary>
              <code>{{ t.name }}</code>
              <span class="tdesc">{{ t.description }}</span>
            </summary>
            <ul v-if="t.params.length" class="params">
              <li v-for="p in t.params" :key="p.name">
                <code>{{ p.name }}</code><span v-if="p.required" class="chip accent">required</span>
                <span class="muted small">{{ p.description }}</span>
              </li>
            </ul>
            <p v-else class="muted small">No parameters.</p>
          </details>
          <h3 class="sub">Resources</h3>
          <ul class="plain">
            <li v-for="r in info.data.value.catalog.resources" :key="r.name"><code>{{ r.name }}</code> <span class="muted small">{{ r.description }}</span></li>
          </ul>
          <h3 class="sub">Prompts</h3>
          <ul class="plain">
            <li v-for="p in info.data.value.catalog.prompts" :key="p.name"><code>{{ p.name }}</code> <span class="muted small">{{ p.description }}</span></li>
          </ul>
        </div>
      </StateBlock>
      <p class="muted small">Manage and revoke keys under <RouterLink to="/settings/connections">Settings → Connections</RouterLink>.</p>
    </Card>
  </div>
</template>

<style scoped>
.endpoint {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.nowrap {
  flex-wrap: nowrap;
  gap: 6px;
}
.nowrap code {
  overflow-wrap: anywhere;
  min-width: 0;
}
.endpoint p {
  margin: 0;
}
.keys {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.opt {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.opt.on {
  border-color: var(--accent);
  background: var(--elevated);
}
.opt input {
  width: 18px;
  height: 18px;
  accent-color: var(--accent);
  flex: none;
}
.grow {
  flex: 1;
  min-width: 0;
}
.block {
  display: block;
}
.create {
  margin-top: 4px;
}
.secret {
  padding: 10px 12px;
  border: 1px solid var(--attention);
  border-radius: var(--radius-sm);
  background: var(--attention-bg);
}
.secret p {
  margin: 0 0 6px;
}
.tabs {
  display: flex;
  gap: 2px;
  padding: 3px;
  overflow-x: auto;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  scrollbar-width: none;
}
.tabs button {
  flex: none;
  min-height: 38px;
  padding: 0 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-2);
  font-weight: 560;
  font-size: 0.875rem;
  cursor: pointer;
}
.tabs button.on {
  background: var(--elevated);
  color: var(--text);
  box-shadow: 0 0 0 1px var(--border-strong);
}
.snippet {
  position: relative;
}
.snippet pre {
  margin: 0;
  padding-right: 88px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.copybtn {
  position: absolute;
  top: 8px;
  right: 8px;
}
.examples,
.plain,
.params {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.examples li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.tools {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.tool {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0 12px;
}
.tool summary {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 0;
  cursor: pointer;
  list-style: none;
}
.tool summary::-webkit-details-marker {
  display: none;
}
.tool[open] summary {
  border-bottom: 1px solid var(--border);
}
.tdesc {
  color: var(--text-2);
  font-size: 0.85rem;
}
.params {
  padding: 10px 0;
}
.params li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 8px;
}
.sub {
  font-size: 0.95rem;
  margin: 10px 0 0;
}
</style>
