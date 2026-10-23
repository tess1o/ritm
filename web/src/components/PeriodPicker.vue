<script setup lang="ts">
// Period toolbar: presets · ‹ range › (with the comparison range beneath) · Compare switch.
// The range opens a calendar that marks the days with data; picking two days makes a custom range.
import DataCalendar from './DataCalendar.vue'
import Icon from './Icon.vue'
import { usePeriod, type PeriodKey } from '../composables/usePeriod'
import { range as fmtRange, today } from '../lib/fmt'

withDefaults(defineProps<{ compare?: boolean }>(), { compare: true })
const { state, range, previous, shift } = usePeriod()
const options: { key: PeriodKey; label: string; title: string }[] = [
  { key: 'week', label: '7D', title: 'Last 7 days' },
  { key: 'month', label: '30D', title: 'Last 30 days' },
  { key: '3months', label: '3M', title: 'Last 3 months' },
  { key: 'year', label: '1Y', title: 'Last 12 months' },
  { key: 'custom', label: 'Custom', title: 'Custom range' },
]
function select(k: PeriodKey) {
  state.value = { ...state.value, key: k, from: range.value.from, to: range.value.to }
}
function pickRange(from: string, to: string) {
  state.value = { ...state.value, key: 'custom', from, to }
}
function toggleCompare() {
  state.value = { ...state.value, compare: !state.value.compare }
}
</script>

<template>
  <div class="toolbar">
    <div class="seg" role="radiogroup" aria-label="Period">
      <button v-for="o in options" :key="o.key" type="button" role="radio" :aria-checked="state.key === o.key" :title="o.title"
        :class="{ on: state.key === o.key }" @click="select(o.key)">{{ o.label }}</button>
    </div>

    <div class="nav">
      <button class="arrow" type="button" aria-label="Previous period" @click="shift(-1)"><Icon name="left" :size="18" /></button>
      <DataCalendar mode="range" :from="range.from" :to="range.to" :label="`Choose dates, ${fmtRange(range.from, range.to)} shown`"
        @pick-range="pickRange">
        <span class="dates">
          <span class="cur tnum">{{ fmtRange(range.from, range.to) }}<Icon name="calendar" :size="13" class="cal" /></span>
          <span v-if="compare && state.compare" class="prev tnum">vs {{ fmtRange(previous.from, previous.to) }}</span>
        </span>
      </DataCalendar>
      <button class="arrow" type="button" aria-label="Next period" :disabled="range.to >= today()" @click="shift(1)">
        <Icon name="right" :size="18" />
      </button>
    </div>

    <button v-if="compare" class="switch" type="button" role="switch" :aria-checked="state.compare" title="Compare with the previous period of the same length"
      @click="toggleCompare">
      <span class="track"><span class="knob" /></span>
      Compare
    </button>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 10px;
  min-width: 0;
}
.seg,
.nav,
.switch {
  height: 44px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.seg {
  display: inline-flex;
  align-items: center;
  padding: 3px;
  gap: 2px;
}
.seg button {
  height: 36px;
  min-width: 44px;
  padding: 0 10px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-2);
  font-weight: 560;
  font-size: 0.875rem;
  cursor: pointer;
}
.seg button:hover {
  color: var(--text);
}
.seg button.on {
  background: var(--elevated);
  color: var(--text);
  box-shadow: 0 0 0 1px var(--border-strong);
}
.nav {
  display: inline-flex;
  align-items: center;
}
.arrow {
  display: grid;
  place-items: center;
  width: 40px;
  height: 100%;
  border: 0;
  background: transparent;
  color: var(--text-2);
  cursor: pointer;
}
.arrow:hover:not(:disabled) {
  color: var(--text);
}
.arrow:disabled {
  opacity: 0.35;
  cursor: default;
}
.nav :deep(.dc-trigger) {
  padding: 0 8px;
}
.dates {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-width: 132px;
  line-height: 1.15;
}
.cur {
  display: inline-flex;
  align-items: center;
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--text);
  white-space: nowrap;
}
.prev {
  font-size: 0.72rem;
  color: var(--muted);
  white-space: nowrap;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  color: var(--text-2);
  font-size: 0.875rem;
  font-weight: 560;
  cursor: pointer;
}
.switch:hover {
  color: var(--text);
}
.track {
  position: relative;
  width: 30px;
  height: 18px;
  border-radius: 999px;
  background: var(--border-strong);
  transition: background 0.15s ease;
}
.knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text);
  transition: transform 0.15s ease;
}
.switch[aria-checked='true'] {
  color: var(--text);
}
.switch[aria-checked='true'] .track {
  background: var(--accent);
}
.switch[aria-checked='true'] .knob {
  transform: translateX(12px);
  background: var(--on-accent);
}
.cal {
  margin-left: 5px;
  color: var(--muted);
}
</style>
