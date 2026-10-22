<script setup lang="ts">
import Card from '../../components/Card.vue'
import { useAsync } from '../../composables/useAsync'
import { useSettingsDraft } from '../../composables/useSettingsDraft'
import { get } from '../../lib/api'
import { num } from '../../lib/fmt'
import type { Items, WorkoutType } from '../../lib/types'
import SaveBar from './SaveBar.vue'

const { form, res, dirty, saving, submit, discard } = useSettingsDraft()
const types = useAsync((s) => get<Items<WorkoutType>>('/workout-types', s))

const zonePct = (i: number) => Math.round((form.value?.zone_bounds[i] ?? 0) * 100)
function setZone(i: number, v: string) {
  if (form.value) form.value.zone_bounds[i] = Number(v) / 100
}
function numOrNull(v: string) {
  return v === '' ? null : Number(v)
}
</script>

<template>
  <form v-if="form && res" class="sform" @submit.prevent="submit">
    <Card title="Heart-rate zones" :subtitle="`Max HR ${num(res.max_hr_bpm)} bpm (${res.max_hr_source}) · resting ${num(res.resting_hr_bpm)} bpm (${res.resting_hr_source.replace('_', ' ')})`">
      <div class="form-grid">
        <label class="field"><span>Zone model</span>
          <select v-model="form.zone_model" class="input">
            <option value="karvonen">Karvonen (% heart-rate reserve)</option>
            <option value="hrmax">% of max HR</option>
          </select></label>
        <label class="field"><span>Max HR (blank = 208 − 0.7 × age)</span>
          <input :value="form.max_hr ?? ''" class="input" type="number" min="100" max="230" inputmode="numeric" :placeholder="String(res.max_hr_bpm)"
            @input="(e) => (form!.max_hr = numOrNull((e.target as HTMLInputElement).value))" /></label>
        <label class="field"><span>Resting HR (blank = 30-day median)</span>
          <input :value="form.resting_hr ?? ''" class="input" type="number" min="25" max="120" inputmode="numeric" :placeholder="String(res.resting_hr_bpm)"
            @input="(e) => (form!.resting_hr = numOrNull((e.target as HTMLInputElement).value))" /></label>
      </div>
      <div class="zones">
        <div v-for="i in 5" :key="i" class="zone">
          <span class="zdot" :style="{ background: `var(--zone${i})` }" />
          <span class="small">Zone {{ i }}</span>
          <input class="input" type="number" min="1" max="99" :value="zonePct(i - 1)" :aria-label="`Zone ${i} lower bound in percent`"
            @input="(e) => setZone(i - 1, (e.target as HTMLInputElement).value)" />
          <span class="muted small tnum">% · ≥{{ num(res.zone_lower_bpm[i - 1]) }} bpm</span>
        </div>
      </div>
    </Card>

    <Card title="Training" subtitle="How workouts turn into training load">
      <div class="form-grid">
        <label class="field"><span>Load model</span>
          <select v-model="form.load_model" class="input">
            <option value="edwards">Edwards TRIMP (zones × minutes)</option>
            <option value="banister">Banister TRIMP</option>
            <option value="srpe">sRPE (RPE × minutes; unrated sessions estimated from TRIMP)</option>
          </select></label>
        <label class="field"><span>Main workout type</span>
          <select v-model="form.main_workout_type" class="input">
            <option value="">Auto — {{ res.main_workout_name }}</option>
            <option v-for="t in types.data.value?.items" :key="t.activity_type" :value="t.activity_type">{{ t.name }} ({{ t.count }})</option>
          </select></label>
      </div>
    </Card>

    <SaveBar :dirty="dirty" :saving="saving" @discard="discard" />
  </form>
</template>

<style scoped>
.zones {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.zone {
  display: grid;
  grid-template-columns: 12px 60px 80px 1fr;
  align-items: center;
  gap: 8px;
}
.zone .input {
  min-height: 38px;
  padding: 4px 8px;
}
.zdot {
  width: 12px;
  height: 12px;
  border-radius: 50%;
}
</style>
