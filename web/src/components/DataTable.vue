<script setup lang="ts" generic="T extends object">
// Table on wide screens, card list on phones (UI-M5).
export interface Column<R> {
  key: string
  label: string
  value: (row: R) => string
  align?: 'left' | 'right'
  primary?: boolean
}
defineProps<{ rows: T[]; columns: Column<T>[]; rowKey: (row: T) => string | number; empty?: string }>()
defineEmits<{ select: [row: T] }>()
// `cell-<key>` replaces a column's text with custom markup, in both the table and the card list.
defineSlots<Record<`cell-${string}`, (props: { row: T }) => unknown>>()
</script>

<template>
  <div class="dt">
    <p v-if="!rows.length" class="muted small empty">{{ empty ?? 'No data for this period.' }}</p>
    <table v-else class="wide">
      <thead>
        <tr>
          <th v-for="c in columns" :key="c.key" :class="c.align">{{ c.label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="rowKey(r)" tabindex="0" @click="$emit('select', r)" @keydown.enter="$emit('select', r)">
          <td v-for="c in columns" :key="c.key" :class="[c.align, 'tnum']">
            <slot :name="`cell-${c.key}`" :row="r">{{ c.value(r) }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
    <ul v-if="rows.length" class="cards">
      <li v-for="r in rows" :key="rowKey(r)">
        <button type="button" @click="$emit('select', r)">
          <div class="primary">
            <template v-for="c in columns.filter((x) => x.primary)" :key="c.key">
              <span><slot :name="`cell-${c.key}`" :row="r">{{ c.value(r) }}</slot></span>
            </template>
          </div>
          <dl>
            <template v-for="c in columns.filter((x) => !x.primary)" :key="c.key">
              <div>
                <dt>{{ c.label }}</dt>
                <dd class="tnum">{{ c.value(r) }}</dd>
              </div>
            </template>
          </dl>
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.empty {
  padding: 8px 0;
}
.wide {
  display: none;
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}
th {
  text-align: left;
  font-weight: 560;
  color: var(--muted);
  font-size: 0.8rem;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
td {
  padding: 10px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
tbody tr {
  cursor: pointer;
}
tbody tr:hover,
tbody tr:focus-visible {
  background: var(--elevated);
}
.right {
  text-align: right;
}
.cards {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.cards button {
  width: 100%;
  text-align: left;
  background: var(--elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 12px;
  cursor: pointer;
}
.primary {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-weight: 600;
  margin-bottom: 6px;
}
dl {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 6px 10px;
}
dt {
  font-size: 0.72rem;
  color: var(--muted);
}
dd {
  margin: 0;
  font-size: 0.9rem;
}
@media (min-width: 640px) {
  .wide {
    display: table;
  }
  .cards {
    display: none;
  }
}
</style>
