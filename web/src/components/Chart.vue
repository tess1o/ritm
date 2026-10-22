<script setup lang="ts">
// One reusable ECharts wrapper: init, dark theme, resize via ResizeObserver, disposal (FE-6).
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { echarts, ensureTheme } from '../charts/echarts'
import type { EChartsCoreOption } from 'echarts/core'

const props = withDefaults(defineProps<{ option: EChartsCoreOption; height?: number; label: string }>(), { height: 240 })
const emit = defineEmits<{ click: [params: unknown] }>()
const el = ref<HTMLDivElement | null>(null)
const chart = shallowRef<echarts.ECharts | null>(null)
let ro: ResizeObserver | null = null

// Keep the legend on a single scrollable row and leave room below it for the axis names
// (e.g. "bpm"), which ECharts draws above the plot area and would otherwise overlap the legend.
const LEGEND_TOP_SPACE = 56
function layout(o: EChartsCoreOption): EChartsCoreOption {
  const opt = o as Record<string, unknown>
  if (!opt.legend) return o
  const legend = { type: 'scroll', top: 0, ...(opt.legend as object) }
  const grids = Array.isArray(opt.grid) ? [...opt.grid] : [opt.grid ?? {}]
  const first = { ...(grids[0] as Record<string, unknown>) }
  first.top = Math.max(Number(first.top ?? 0), LEGEND_TOP_SPACE)
  grids[0] = first
  return { ...opt, legend, grid: Array.isArray(opt.grid) ? grids : first }
}

onMounted(() => {
  ensureTheme()
  const c = echarts.init(el.value!, 'ritm', { renderer: 'canvas' })
  c.setOption(layout(props.option), true)
  c.on('click', (p) => emit('click', p))
  chart.value = c
  ro = new ResizeObserver(() => c.resize())
  ro.observe(el.value!)
})
watch(
  () => props.option,
  (o) => chart.value?.setOption(layout(o), true),
)
onBeforeUnmount(() => {
  ro?.disconnect()
  chart.value?.dispose()
})
</script>

<template>
  <div ref="el" class="chart" :style="{ height: `${height}px` }" role="img" :aria-label="label" />
</template>

<style scoped>
.chart {
  width: 100%;
  min-width: 0;
}
</style>
