<script setup lang="ts">
import { computed, ref } from 'vue'
import Card from '../../components/Card.vue'
import Icon from '../../components/Icon.vue'
import StateBlock from '../../components/StateBlock.vue'
import { useAsync } from '../../composables/useAsync'
import { get } from '../../lib/api'
import { bytes, num } from '../../lib/fmt'
import type { Coverage } from '../../lib/types'
import DangerModal from './DangerModal.vue'

const coverage = useAsync((s) => get<Coverage>('/coverage', s))
const resetting = ref(false)

const KINDS = [
  { kind: 'hourly', label: 'Hourly activity' },
  { kind: 'record', label: 'Records' },
  { kind: 'summary', label: 'Daily summaries' },
  { kind: 'workout', label: 'Workouts' },
] as const

const types = computed(() => coverage.data.value?.types ?? [])
const groups = computed(() =>
  KINDS.map((k) => ({ ...k, rows: types.value.filter((t) => t.kind === k.kind).sort((a, b) => a.name.localeCompare(b.name)) })).filter((g) => g.rows.length),
)
const span = computed(() => ({
  from: types.value.map((t) => t.from).sort()[0] ?? '—',
  to: types.value.map((t) => t.to).sort().at(-1) ?? '—',
}))
const tiles = computed(() => {
  const c = coverage.data.value
  if (!c) return []
  return [
    { label: 'Database', value: bytes(c.db_bytes), note: `+ ${bytes(c.wal_bytes)} WAL` },
    { label: 'Samples', value: num(c.row_counts.records), note: `${num(c.row_counts.hourly_activity)} hourly buckets` },
    { label: 'Workouts', value: num(c.row_counts.workouts), note: `${types.value.length} data types` },
    { label: 'Since', value: span.value.from, note: `to ${span.value.to} · ${c.sources.length} sources` },
  ]
})
</script>

<template>
  <Card title="Data coverage" subtitle="What is stored, per data type">
    <StateBlock :loading="coverage.loading.value" :error="coverage.error.value" :empty="!types.length" empty-text="No data imported yet.">
      <dl class="tiles-cov">
        <div v-for="t in tiles" :key="t.label">
          <dt>{{ t.label }}</dt>
          <dd class="tnum">{{ t.value }}</dd>
          <dd class="muted small">{{ t.note }}</dd>
        </div>
      </dl>
      <details class="types">
        <summary class="small">All {{ types.length }} data types</summary>
        <div class="covtable">
          <table>
            <thead><tr><th>Type</th><th>From</th><th>To</th><th class="r">Rows</th></tr></thead>
            <tbody v-for="g in groups" :key="g.kind">
              <tr class="ghead"><th colspan="4">{{ g.label }} <span class="muted">· {{ g.rows.length }}</span></th></tr>
              <tr v-for="t in g.rows" :key="t.kind + t.type">
                <td>{{ t.name }}</td>
                <td class="tnum">{{ t.from }}</td>
                <td class="tnum">{{ t.to }}</td>
                <td class="tnum r">{{ num(t.count) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </StateBlock>
    <p class="muted small">To add data, use <RouterLink to="/import">Import</RouterLink>.</p>
  </Card>

  <Card title="Danger zone">
    <div class="danger-row">
      <div>
        <strong>Delete all data</strong>
        <p class="muted small">Removes every import, sample, workout, insight, note and setting. Your account and API keys stay.</p>
      </div>
      <button class="btn danger" type="button" @click="resetting = true"><Icon name="trash" :size="16" /> Delete data</button>
    </div>
  </Card>

  <DangerModal v-if="resetting" kind="reset" @close="resetting = false" />
</template>

<style scoped>
.tiles-cov {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
  margin: 0;
}
.tiles-cov div {
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--elevated);
}
.tiles-cov dt {
  font-size: 0.8rem;
  color: var(--text-2);
}
.tiles-cov dd {
  margin: 2px 0 0;
}
.tiles-cov dd.tnum {
  font-size: 1.05rem;
  font-weight: 600;
}
.types summary {
  cursor: pointer;
  color: var(--accent);
  padding: 4px 0;
}
.covtable {
  overflow-x: auto;
  margin-top: 8px;
}
.covtable table {
  border-collapse: collapse;
  width: 100%;
  font-size: 0.85rem;
}
.covtable th,
.covtable td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
.covtable .r {
  text-align: right;
}
.ghead th {
  padding-top: 14px;
  font-size: 0.75rem;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-2);
}
</style>
