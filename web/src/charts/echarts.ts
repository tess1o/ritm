// Tree-shaken ECharts: only the modules Ritm uses are registered.
import * as echarts from 'echarts/core'
import { BarChart, HeatmapChart, LineChart, ScatterChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  MarkAreaComponent,
  MarkLineComponent,
  TooltipComponent,
  VisualMapComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { cssVar } from '../lib/css'

echarts.use([
  LineChart,
  BarChart,
  HeatmapChart,
  ScatterChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  MarkLineComponent,
  MarkAreaComponent,
  VisualMapComponent,
  CanvasRenderer,
])

let registered = false

/** Dark theme built from the design tokens. */
export function ensureTheme() {
  if (registered) return
  registered = true
  const text2 = cssVar('--text-2')
  const muted = cssVar('--muted')
  const border = cssVar('--border')
  const axis = {
    axisLine: { lineStyle: { color: border } },
    axisTick: { lineStyle: { color: border } },
    axisLabel: { color: muted, fontSize: 11 },
    splitLine: { lineStyle: { color: border, opacity: 0.6 } },
    nameTextStyle: { color: muted, fontSize: 11 },
  }
  echarts.registerTheme('ritm', {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: cssVar('--font'), color: text2 },
    color: ['--s-blue', '--s-orange', '--s-aqua', '--s-yellow', '--s-magenta', '--s-green', '--s-violet', '--s-red'].map(cssVar),
    categoryAxis: { ...axis, splitLine: { show: false } },
    valueAxis: axis,
    timeAxis: { ...axis, splitLine: { show: false } },
    legend: { textStyle: { color: text2 }, inactiveColor: border },
    tooltip: {
      backgroundColor: cssVar('--elevated'),
      borderColor: cssVar('--border-strong'),
      textStyle: { color: cssVar('--text'), fontSize: 12 },
      confine: true,
      extraCssText: 'border-radius:9px;box-shadow:0 8px 24px rgba(0,0,0,.45)',
    },
  })
}

export { echarts }
