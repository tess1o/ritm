<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Card from '../components/Card.vue'
import FileDrop from '../components/FileDrop.vue'
import Icon from '../components/Icon.vue'
import ProgressBar from '../components/ProgressBar.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { useFreshness } from '../composables/useFreshness'
import { useImport } from '../composables/useImport'
import { useIphoneShortcut, useKeys } from '../composables/useKeys'
import { useSettings } from '../composables/useSettings'
import { useSleepAvailable } from '../composables/useSleepAvailable'
import { get } from '../lib/api'
import { ago, bytes, dateTime, num, typeName } from '../lib/fmt'
import type { ImportRow, ImportStats, Items } from '../lib/types'
import { SHORTCUT_NAME } from './settings/clients'
import ConnectionGuide from './settings/ConnectionGuide.vue'

const imp = useImport()
const full = ref(false)
const history = useAsync((s) => get<Items<ImportRow>>('/imports', s))
const settings = useSettings()
const sleep = useSleepAvailable()
const freshness = useFreshness()

const onVisible = () => document.visibilityState === 'visible' && imp.resume()
onMounted(() => {
  imp.resume()
  document.addEventListener('visibilitychange', onVisible)
})
onUnmounted(() => document.removeEventListener('visibilitychange', onVisible))
watch(imp.phase, (p) => {
  if (p === 'done' || p === 'failed') {
    history.reload()
    settings.load(true)
    sleep.check(true)
    freshness.load(true)
  }
})

const pct = computed(() => (imp.phase.value === 'uploading' ? imp.uploadPct.value : (imp.progress.value?.percent ?? 0)))
const phaseLabel = computed(() => {
  switch (imp.phase.value) {
    case 'uploading':
      return `Uploading… ${num(imp.uploadPct.value)} %`
    case 'processing':
      if (imp.progress.value?.phase === 'queued') return 'Queued — another import is running on this server…'
      return imp.progress.value?.phase === 'recompute'
        ? 'Updating dashboards and insights…'
        : `Reading export… ${num(imp.progress.value?.percent ?? 0)} % of ${bytes(imp.progress.value?.total_bytes ?? 0)}`
    default:
      return ''
  }
})

function totals(s?: ImportStats) {
  if (!s) return null
  let ins = 0
  let dup = 0
  let below = 0
  for (const t of Object.values(s.types)) {
    ins += t.inserted
    dup += t.duplicate
    below += t.below_cutoff
  }
  return { ins, dup, below }
}
// The iPhone Shortcut: the easier way in, so it leads the page. /import?connect=iphone (the nav item) opens its guide.
const iphone = useIphoneShortcut()
iphone.load()
const { created, creating, create } = useKeys()
const guide = ref(false)
const baseURL = computed(() => String(settings.data.value?.server.base_url || window.location.origin))
function openGuide() {
  settings.load()
  created.value = null
  guide.value = true
}
function closeGuide() {
  guide.value = false
  created.value = null
}
const route = useRoute()
const router = useRouter()
watch(
  () => route.query.connect,
  (c) => {
    if (c !== 'iphone') return
    openGuide()
    router.replace({ query: {} })
  },
  { immediate: true },
)

const latest = computed(() => imp.result.value ?? history.data.value?.items[0] ?? null)
const showTypes = ref(false)
</script>

<template>
  <div class="page">
    <section v-if="iphone.known.value && !iphone.connected.value" class="shortcut">
      <span class="sicon"><Icon name="phone" :size="22" /></span>
      <div class="stext">
        <strong>Send exports straight from your iPhone</strong>
        <p class="small text-2">Add the <em>{{ SHORTCUT_NAME }}</em> shortcut once, then pick it in the Health share sheet — no computer, no
          file to move. Takes a minute to set up.</p>
      </div>
      <button class="btn primary" type="button" @click="openGuide"><Icon name="phone" :size="16" /> Connect iPhone</button>
    </section>
    <div v-else-if="iphone.connected.value" class="shortcut connected small">
      <span class="chip positive"><Icon name="check" :size="12" /> iPhone Shortcut connected</span>
      <span class="muted">{{ iphone.lastUsed.value ? `last upload ${ago(iphone.lastUsed.value)}` : 'no upload yet' }}</span>
      <span class="spacer" />
      <button class="btn small ghost" type="button" @click="openGuide">Setup</button>
      <RouterLink :to="{ name: 'settings-connections' }" class="btn small ghost">Manage</RouterLink>
    </div>

    <div class="cols-2">
      <Card title="Upload Apple Health export">
        <FileDrop :disabled="imp.busy.value" @file="(f) => imp.start(f, full)" />
        <label class="check">
          <input v-model="full" type="checkbox" :disabled="imp.busy.value" />
          <span>Full re-import <span class="muted small">— ignore what was imported before (slower)</span></span>
        </label>
        <div v-if="imp.busy.value" class="progress">
          <ProgressBar :value="pct" :label="phaseLabel" />
          <div class="row">
            <span class="small text-2">{{ phaseLabel }}</span>
            <span class="spacer" />
            <button v-if="imp.phase.value === 'uploading'" class="btn small" type="button" @click="imp.cancelUpload">Cancel</button>
          </div>
          <p class="muted small">You can leave this page — the import continues on the server.</p>
        </div>
        <p v-if="imp.error.value" class="error-box small" role="alert">{{ imp.error.value }}</p>
      </Card>

      <Card title="How to export">
        <ol class="steps text-2">
          <li>On the iPhone open <strong>Health</strong>, tap your profile picture.</li>
          <li>Tap <strong>Export All Health Data</strong> and wait (can take a few minutes).</li>
          <li>Choose <strong>Save to Files</strong> (e.g. iCloud Drive), or AirDrop it to your computer.</li>
          <li>Upload <code>export.zip</code> here. Re-uploading a newer full export only adds new data.</li>
        </ol>
        <p v-if="iphone.connected.value" class="small text-2">Or tap <strong>{{ SHORTCUT_NAME }}</strong> in the share sheet in step 3.</p>
      </Card>
    </div>

    <Card v-if="latest" :title="latest.status === 'failed' ? 'Last import failed' : 'Last import'">
      <p v-if="latest.error" class="error-box small">{{ latest.error }}</p>
      <template v-if="latest.stats">
        <div class="summary">
          <div><span class="muted small">Export date</span><strong>{{ latest.exported_at ? dateTime(latest.exported_at) : '–' }}</strong></div>
          <div><span class="muted small">Duration</span><strong>{{ num(latest.stats.duration_ms / 1000, 1) }} s</strong></div>
          <div><span class="muted small">New records</span><strong>{{ num(totals(latest.stats)?.ins) }}</strong></div>
          <div><span class="muted small">Already known</span><strong>{{ num((totals(latest.stats)?.dup ?? 0) + (totals(latest.stats)?.below ?? 0)) }}</strong></div>
          <div><span class="muted small">New workouts</span><strong>{{ latest.stats.new_workouts }}</strong></div>
          <div><span class="muted small">Days re-aggregated</span><strong>{{ latest.stats.days_reaggregated }}</strong></div>
        </div>
        <p v-for="w in latest.stats.warnings ?? []" :key="w" class="chip attention">{{ w }}</p>
        <details v-if="latest.stats.errors?.length" class="small">
          <summary>Skipped malformed elements (first {{ latest.stats.errors.length }})</summary>
          <ul class="problems muted">
            <li v-for="e in latest.stats.errors" :key="e"><code>{{ e }}</code></li>
          </ul>
        </details>
        <button class="btn small ghost" type="button" @click="showTypes = !showTypes">{{ showTypes ? 'Hide' : 'Show' }} per-type details</button>
        <div v-if="showTypes" class="types">
          <table>
            <thead>
              <tr><th>Type</th><th>Scanned</th><th>Inserted</th><th>Duplicate</th><th>Below cutoff</th><th>Skipped</th></tr>
            </thead>
            <tbody>
              <tr v-for="(t, k) in latest.stats.types" :key="k">
                <td>{{ typeName(String(k)) }}</td>
                <td class="tnum">{{ num(t.scanned) }}</td>
                <td class="tnum">{{ num(t.inserted) }}</td>
                <td class="tnum">{{ num(t.duplicate) }}</td>
                <td class="tnum">{{ num(t.below_cutoff) }}</td>
                <td class="tnum">{{ num(t.skipped) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </Card>

    <Card title="History">
      <StateBlock :loading="history.loading.value" :error="history.error.value" :empty="!history.data.value?.items.length" empty-text="No imports yet.">
        <ul class="hist">
          <li v-for="r in history.data.value?.items" :key="r.id">
            <span class="chip" :class="r.status === 'succeeded' ? 'positive' : r.status === 'failed' ? 'attention' : 'accent'">{{ r.status }}</span>
            <span class="name">{{ r.file_name }}</span>
            <span class="muted small tnum">{{ r.started_at ? dateTime(r.started_at) : '' }} · {{ bytes(r.file_size) }}{{ r.full_reimport ? ' · full' : '' }}</span>
          </li>
        </ul>
      </StateBlock>
    </Card>

    <ConnectionGuide v-if="guide" scope="ingest" :base="baseURL" :shortcut-url="settings.data.value?.server.shortcut_url ?? ''"
      :secret="created?.secret ?? null" :busy="creating" :active="iphone.active.value.length"
      @generate="(name) => create(name, 'ingest')" @close="closeGuide" />
  </div>
</template>

<style scoped>
.shortcut {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  padding: 14px 16px;
  background: var(--accent-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.shortcut.connected {
  gap: 8px;
  padding: 8px 12px;
  background: var(--surface);
}
.sicon {
  display: grid;
  place-items: center;
  flex: none;
  width: 42px;
  height: 42px;
  border-radius: 11px;
  background: var(--surface);
  color: var(--accent);
}
.stext {
  flex: 1 1 260px;
  min-width: 0;
}
.stext p {
  margin: 2px 0 0;
}
@media (max-width: 520px) {
  .sicon {
    display: none;
  }
}
.progress {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.steps {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.summary div {
  display: flex;
  flex-direction: column;
}
.types {
  overflow-x: auto;
}
.types table {
  border-collapse: collapse;
  font-size: 0.85rem;
  width: 100%;
}
.types th,
.types td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
  text-align: right;
  white-space: nowrap;
}
.types th:first-child,
.types td:first-child {
  text-align: left;
}
.hist {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.hist li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.name {
  font-weight: 560;
  overflow-wrap: anywhere;
}
@media (min-width: 640px) {
  .summary {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
.problems {
  margin: 6px 0 0;
  padding-left: 18px;
  overflow-wrap: anywhere;
}
</style>
