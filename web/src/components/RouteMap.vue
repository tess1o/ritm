<script setup lang="ts">
// GPS route of an outdoor workout on a street map, darkened to match the theme (UI-D6). Leaflet is only loaded with this
// component, which Workout detail imports lazily for workouts that have a route.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
// The ES module build: the default (UMD) entry would pull a CommonJS-interop chunk shared with ECharts.
import * as L from 'leaflet/dist/leaflet-src.esm.js'
import 'leaflet/dist/leaflet.css'
import { clock, time } from '../lib/fmt'
import type { WorkoutRoute } from '../lib/types'

const props = defineProps<{ route: WorkoutRoute; pauses: number[][] }>() // [start_ms, end_ms] pairs

// Design-token colour, defined here: lib/css is bundled with the chart code, and importing it
// would pull ECharts into this lazily loaded chunk.
const color = (token: string) => getComputedStyle(document.documentElement).getPropertyValue(token).trim()

const el = ref<HTMLElement | null>(null)
let map: L.Map | null = null
let layer: L.LayerGroup | null = null
let ro: ResizeObserver | null = null

// Pace buckets relative to this workout (fast → slow); the legend names their ranges, so the
// colour is never the only cue (UI-D5).
const paceTokens = ['--pace1', '--pace2', '--pace3', '--pace4', '--pace5']

const buckets = computed(() => {
  const paces = props.route.track.map((p) => p.pace_s_per_km).filter((p): p is number => p != null).sort((a, b) => a - b)
  if (paces.length < 2) return [] as number[]
  const q = (f: number) => paces[Math.min(paces.length - 1, Math.floor(f * paces.length))]
  return [q(0.2), q(0.4), q(0.6), q(0.8)] // upper bounds of buckets 1–4
})

function bucketOf(pace: number) {
  const i = buckets.value.findIndex((b) => pace <= b)
  return i < 0 ? buckets.value.length : i
}

const legend = computed(() => {
  const b = buckets.value
  if (!b.length) return []
  const lo = Math.min(...props.route.track.map((p) => p.pace_s_per_km ?? Infinity))
  const edges = [lo, ...b]
  return paceTokens.map((token, i) => ({
    token,
    label: i < b.length ? `${clock(edges[i])}–${clock(b[i])}` : `> ${clock(b[b.length - 1])}`,
  }))
})

function draw() {
  if (!map) return
  layer?.remove()
  layer = L.layerGroup().addTo(map)
  const track = props.route.track
  const ll = (i: number): L.LatLngTuple => [track[i].lat, track[i].lon]

  // Consecutive points in the same pace bucket share one polyline.
  let run: L.LatLngTuple[] = [ll(0)]
  let runKey = -2
  const flush = () => {
    if (run.length < 2) return
    const style: L.PolylineOptions =
      runKey === -1
        ? { color: color('--c-context'), weight: 4, dashArray: '4 6' } // stopped / no timestamps
        : { color: color(paceTokens[runKey] ?? '--accent'), weight: 5 }
    L.polyline(run, { ...style, opacity: 0.95, lineCap: 'round', lineJoin: 'round', interactive: false }).addTo(layer!)
  }
  for (let i = 1; i < track.length; i++) {
    const p = track[i].pace_s_per_km
    const key = p == null ? -1 : buckets.value.length ? bucketOf(p) : 2
    if (key !== runKey) {
      flush()
      run = [ll(i - 1)]
      runKey = key
    }
    run.push(ll(i))
  }
  flush()

  const dot = (i: number, token: string, label: string) =>
    L.circleMarker(ll(i), { radius: 7, color: color('--bg'), weight: 2, fillColor: color(token), fillOpacity: 1 })
      .bindTooltip(label)
      .addTo(layer!)
  dot(0, '--positive', `Start ${time(track[0].t_ms)}`)
  dot(track.length - 1, '--danger', `Finish ${time(track[track.length - 1].t_ms)}`)
  for (const [a, b] of props.pauses) {
    let best = 0
    for (let i = 1; i < track.length; i++) {
      if (Math.abs(track[i].t_ms - a) < Math.abs(track[best].t_ms - a)) best = i
    }
    L.circleMarker(ll(best), { radius: 5, color: color('--text'), weight: 2, fillColor: color('--muted'), fillOpacity: 1 })
      .bindTooltip(`Paused ${clock((b - a) / 1000)} at ${time(a)}`)
      .addTo(layer)
  }

  const bd = props.route.bounds
  map.fitBounds([[bd.min_lat, bd.min_lon], [bd.max_lat, bd.max_lon]], { padding: [24, 24], maxZoom: 17 })
}

onMounted(() => {
  if (!el.value) return
  map = L.map(el.value, { scrollWheelZoom: true, wheelPxPerZoomLevel: 120, zoomControl: true, attributionControl: true })
  // OpenStreetMap's standard tiles need no API key (CARTO's dark tiles now do). Their usage policy
  // asks for a Referer, which the app's same-origin Referrer-Policy would strip; send the origin.
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    referrerPolicy: 'strict-origin-when-cross-origin',
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
  }).addTo(map)
  draw()
  ro = new ResizeObserver(() => map?.invalidateSize())
  ro.observe(el.value)
})

watch(() => [props.route, props.pauses], draw)

onBeforeUnmount(() => {
  ro?.disconnect()
  map?.remove()
  map = null
})
</script>

<template>
  <div>
    <div ref="el" class="map" role="img" :aria-label="`Map of the route, ${(route.distance_m / 1000).toFixed(2)} km`" />
    <ul v-if="legend.length" class="legend" aria-label="Pace per kilometre, fast to slow">
      <li class="muted">Pace /km</li>
      <li v-for="l in legend" :key="l.token"><span class="sw" :style="{ background: `var(${l.token})` }" />{{ l.label }}</li>
      <li><span class="sw stop" />stopped</li>
    </ul>
  </div>
</template>

<style scoped>
.map {
  height: 340px;
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--bg);
  isolation: isolate; /* keep Leaflet's z-indexes below the app's top bar and modals */
}
@media (max-width: 639px) {
  .map {
    height: 260px;
  }
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 10px 0 0;
  padding: 0;
  list-style: none;
  font-size: 0.8rem;
  color: var(--text-2);
}
.legend li {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.sw {
  width: 16px;
  height: 4px;
  border-radius: 2px;
}
.sw.stop {
  background: repeating-linear-gradient(90deg, var(--c-context) 0 4px, transparent 4px 7px);
}
/* The tiles are light; invert them into a muted dark map. Only the tile pane is filtered, so the
   route and markers keep their real colours. */
.map :deep(.leaflet-tile-pane) {
  filter: invert(1) hue-rotate(180deg) brightness(0.9) contrast(0.85) saturate(0.35);
}
/* Leaflet's controls are light by default; match the dark theme. */
.map :deep(.leaflet-bar a) {
  background: var(--elevated);
  color: var(--text);
  border-color: var(--border);
  width: 34px;
  height: 34px;
  line-height: 34px;
}
.map :deep(.leaflet-bar a:hover) {
  background: var(--surface);
}
.map :deep(.leaflet-control-attribution) {
  background: rgba(15, 17, 21, 0.75);
  color: var(--muted);
}
.map :deep(.leaflet-control-attribution a) {
  color: var(--text-2);
}
.map :deep(.leaflet-tooltip) {
  background: var(--elevated);
  color: var(--text);
  border: 1px solid var(--border);
  box-shadow: none;
}
</style>
