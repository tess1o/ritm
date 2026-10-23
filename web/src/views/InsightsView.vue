<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import Card from '../components/Card.vue'
import Icon from '../components/Icon.vue'
import InsightEvidence from '../components/InsightEvidence.vue'
import StateBlock from '../components/StateBlock.vue'
import { useAsync } from '../composables/useAsync'
import { setPageMeta } from '../composables/usePageMeta'
import { usePeriod } from '../composables/usePeriod'
import { usePeriodSwipe } from '../composables/useSwipe'
import { get, qs } from '../lib/api'
import { collapse, group, streakLabel, type Row } from '../lib/insights'
import { today } from '../lib/fmt'
import type { InsightList, Polarity } from '../lib/types'

setPageMeta(() => ({ subtitle: 'Comparisons with your own baseline · informational, not medical advice' }))
const category = ref('')
// Polarity filters on the client, so the summary chips keep showing every count.
const polarity = ref<Polarity | ''>('')
const { range: period, shift } = usePeriod()
const swipe = usePeriodSwipe(shift, () => period.value.to < today())
const list = useAsync(
  (s) =>
    get<InsightList>(
      `/insights${qs({ period: `${period.value.from}..${period.value.to}`, category: category.value, limit: 300 })}`,
      s,
    ),
  [period, category],
)

const rows = computed(() => collapse(list.data.value?.items ?? []))
const counts = computed(() => {
  const c: Record<Polarity, number> = { attention: 0, positive: 0, neutral: 0 }
  for (const r of rows.value) c[r.insight.polarity]++
  return c
})
const groups = computed(() => group(polarity.value ? rows.value.filter((r) => r.insight.polarity === polarity.value) : rows.value, today()))
const visible = computed(() => groups.value.flatMap((g) => g.rows))

const SUMMARY: { key: Polarity; icon: string; label: (n: number) => string }[] = [
  { key: 'attention', icon: 'alert', label: (n) => `${n} need${n === 1 ? 's' : ''} attention` },
  { key: 'positive', icon: 'check', label: (n) => `${n} positive` },
  { key: 'neutral', icon: 'info', label: (n) => `${n} note${n === 1 ? '' : 's'}` },
]
const MARK: Record<Polarity, { icon: string; label: string }> = {
  positive: { icon: 'check', label: 'Positive' },
  neutral: { icon: 'info', label: 'Note' },
  attention: { icon: 'alert', label: 'Attention' },
}

// Wide screens show the evidence of the selected insight in a side pane; narrow ones expand it
// under the row.
const wideQuery = window.matchMedia('(min-width: 1200px)')
const wide = ref(wideQuery.matches)
const onWide = (e: MediaQueryListEvent) => (wide.value = e.matches)
wideQuery.addEventListener('change', onWide)
onScopeDispose(() => wideQuery.removeEventListener('change', onWide))

const open = ref<number | null>(null)
const selected = computed<Row | undefined>(() => visible.value.find((r) => r.insight.id === open.value))
// On wide screens the pane always shows something: the first (most important) insight by default.
watch(
  [visible, wide],
  () => {
    if (wide.value && !selected.value) open.value = visible.value[0]?.insight.id ?? null
  },
  { immediate: true },
)
function toggle(r: Row) {
  open.value = open.value === r.insight.id && !wide.value ? null : r.insight.id
}
</script>

<template>
  <div class="page" @touchstart.passive="swipe.onTouchStart" @touchend.passive="swipe.onTouchEnd">
    <Teleport defer to="#topbar-actions">
      <select v-model="category" class="input filter" aria-label="Category">
        <option value="">All categories</option>
        <option value="activity">Activity</option>
        <option value="heart">Heart</option>
        <option value="training">Training</option>
        <option value="recovery">Recovery</option>
      </select>
    </Teleport>

    <div v-if="rows.length" class="summary" role="group" aria-label="Filter by kind">
      <button v-for="s in SUMMARY" :key="s.key" type="button" class="sum" :class="[s.key, { on: polarity === s.key }]"
        :aria-pressed="polarity === s.key" :disabled="!counts[s.key]" @click="polarity = polarity === s.key ? '' : s.key">
        <Icon :name="s.icon" :size="15" />
        <span class="tnum">{{ s.label(counts[s.key]) }}</span>
      </button>
      <button v-if="polarity" type="button" class="sum clear" @click="polarity = ''">Show all</button>
    </div>

    <StateBlock :loading="list.loading.value" :error="list.error.value" :empty="!groups.length" :height="300"
      :empty-text="polarity ? 'Nothing of this kind in the period.' : 'No insights for this period. They need a few weeks of data, and only notable changes are reported.'">
      <div class="layout" :class="{ wide }">
        <div class="groups">
          <Card v-for="g in groups" :key="g.key" flush>
            <header class="group-head">
              <h2>{{ g.title }}</h2>
              <span class="muted small tnum">{{ g.sub }}</span>
            </header>
            <ul class="rows">
              <li v-for="r in g.rows" :key="r.insight.id" :class="{ open: open === r.insight.id }">
                <button type="button" class="row-btn" :class="r.insight.polarity" :aria-expanded="wide ? undefined : open === r.insight.id"
                  :aria-current="wide && open === r.insight.id ? 'true' : undefined" :aria-controls="wide ? 'ev-pane' : `ev-${r.insight.id}`"
                  @click="toggle(r)">
                  <span class="mark" :title="MARK[r.insight.polarity].label">
                    <Icon :name="MARK[r.insight.polarity].icon" :size="15" :label="MARK[r.insight.polarity].label" />
                  </span>
                  <span class="text">
                    <span class="title">{{ r.insight.title }}</span>
                    <span class="body">{{ r.insight.body }}</span>
                    <span v-if="streakLabel(r)" class="chip streak"><Icon name="flame" :size="13" />{{ streakLabel(r) }}</span>
                  </span>
                  <Icon name="right" :size="16" class="chev" />
                </button>
                <div v-if="!wide && open === r.insight.id" :id="`ev-${r.insight.id}`" class="inline-ev">
                  <InsightEvidence :insight="r.insight" :repeats="r.repeats" />
                </div>
              </li>
            </ul>
          </Card>
          <p v-if="list.data.value?.truncated" class="muted small">More insights match — choose a shorter period to see them all.</p>
        </div>
        <aside v-if="wide && selected" id="ev-pane" class="pane">
          <Card>
            <InsightEvidence :insight="selected.insight" :repeats="selected.repeats" heading @close="open = null" />
          </Card>
        </aside>
      </div>
    </StateBlock>
  </div>
</template>

<style scoped>
.filter {
  width: auto;
  min-height: 44px;
}
.summary {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.sum {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-2);
  font-size: 0.85rem;
  font-weight: 560;
  cursor: pointer;
}
.sum:hover:not(:disabled) {
  border-color: var(--border-strong);
  color: var(--text);
}
.sum:disabled {
  opacity: 0.45;
  cursor: default;
}
.sum.attention :deep(svg) {
  color: var(--attention);
}
.sum.positive :deep(svg) {
  color: var(--positive);
}
.sum.neutral :deep(svg) {
  color: var(--neutral);
}
.sum.on.attention {
  background: var(--attention-bg);
  border-color: var(--attention);
  color: var(--text);
}
.sum.on.positive {
  background: var(--positive-bg);
  border-color: var(--positive);
  color: var(--text);
}
.sum.on.neutral {
  background: var(--neutral-bg);
  border-color: var(--neutral);
  color: var(--text);
}
.sum.clear {
  border-color: transparent;
  background: transparent;
}
.layout {
  display: grid;
  gap: 16px;
  grid-template-columns: minmax(0, 1fr);
}
.layout.wide {
  grid-template-columns: minmax(0, 1fr) minmax(360px, 440px);
  align-items: start;
}
.groups {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}
.pane {
  position: sticky;
  top: 92px; /* below the sticky top bar */
  max-height: calc(100dvh - 108px);
  overflow-y: auto;
  border-radius: var(--radius);
}
.group-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  padding: 14px 14px 6px;
}
.group-head h2 {
  margin: 0;
  font-size: 0.95rem;
}
.rows {
  margin: 0;
  padding: 0 0 6px;
  list-style: none;
}
.rows li + li {
  border-top: 1px solid var(--border);
}
.row-btn {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 56px;
  padding: 10px 14px;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.row-btn:hover {
  background: var(--elevated);
}
.open .row-btn {
  background: var(--elevated);
  box-shadow: inset 3px 0 0 var(--accent);
}
.row-btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.mark {
  flex: none;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--neutral-bg);
  color: var(--neutral);
}
.positive .mark {
  background: var(--positive-bg);
  color: var(--positive);
}
.attention .mark {
  background: var(--attention-bg);
  color: var(--attention);
}
.text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}
.title {
  font-weight: 600;
  color: var(--text);
}
.body {
  font-size: 0.85rem;
  color: var(--text-2);
}
.streak {
  align-self: flex-start;
  margin-top: 4px;
}
.chev {
  flex: none;
  color: var(--muted);
  transition: transform 0.15s ease;
}
.layout:not(.wide) .open .chev {
  transform: rotate(90deg);
}
.inline-ev {
  --ev-tile: var(--surface);
  padding: 4px 14px 16px 54px;
  background: var(--elevated);
}
@media (max-width: 639px) {
  .inline-ev {
    padding-left: 14px;
  }
}
</style>
