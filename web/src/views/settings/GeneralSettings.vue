<script setup lang="ts">
import { computed } from 'vue'
import Card from '../../components/Card.vue'
import { useSettingsDraft } from '../../composables/useSettingsDraft'
import { browserTimeZone, num } from '../../lib/fmt'
import SaveBar from './SaveBar.vue'

const { form, res, server, dirty, saving, submit, discard } = useSettingsDraft()

// Time zone: every IANA zone the browser knows, plus the saved one in case it is spelled differently.
const deviceZone = browserTimeZone()
const timeZones = computed(() => {
  const all = Intl.supportedValuesOf('timeZone')
  const cur = form.value?.timezone
  return cur && !all.includes(cur) ? [cur, ...all] : all
})
</script>

<template>
  <form v-if="form && res" class="sform" @submit.prevent="submit">
    <Card title="Goals" subtitle="Targets for the dashboard rings and weekly summaries">
      <div class="form-grid">
        <label class="field"><span>Steps per day</span>
          <input v-model.number="form.goals.steps_per_day" class="input" type="number" min="1000" step="500" inputmode="numeric" /></label>
        <label class="field"><span>Gym sessions per week</span>
          <input v-model.number="form.goals.sessions_per_week" class="input" type="number" min="1" max="14" inputmode="numeric" /></label>
      </div>
      <label class="check">
        <input v-model="form.goals.use_ring_goals" type="checkbox" />
        <span>Use my Watch ring goals for stand hours and exercise
          <span v-if="form.goals.use_ring_goals && res.goals_source === 'rings'" class="muted small">
            — currently {{ num(res.goals.stand_hours) }} h and {{ num(res.goals.exercise_min) }} min</span>
        </span>
      </label>
      <div v-if="!form.goals.use_ring_goals" class="form-grid">
        <label class="field"><span>Stand hours</span>
          <input v-model.number="form.goals.stand_hours" class="input" type="number" min="1" max="24" inputmode="numeric" /></label>
        <label class="field"><span>Exercise minutes</span>
          <input v-model.number="form.goals.exercise_min" class="input" type="number" min="5" step="5" inputmode="numeric" /></label>
      </div>
    </Card>

    <Card title="Day & time zone" subtitle="When your days start and end, and which hours count as awake">
      <label class="field"><span>Time zone</span>
        <select v-model="form.timezone" class="input">
          <option value="">Server default ({{ server?.timezone }})</option>
          <option v-for="z in timeZones" :key="z" :value="z">{{ z.replaceAll('_', ' ') }}</option>
        </select></label>
      <p v-if="deviceZone && form.timezone !== deviceZone" class="muted small hint">
        This device is in {{ deviceZone.replaceAll('_', ' ') }}.
        <button type="button" class="btn small ghost" @click="form.timezone = deviceZone">Use it</button>
      </p>
      <div class="form-grid">
        <label class="field"><span>Waking hours start</span>
          <input v-model.number="form.waking_start_hour" class="input" type="number" min="0" max="23" inputmode="numeric" /></label>
        <label class="field"><span>Waking hours end</span>
          <input v-model.number="form.waking_end_hour" class="input" type="number" min="1" max="24" inputmode="numeric" /></label>
      </div>
    </Card>

    <SaveBar :dirty="dirty" :saving="saving" @discard="discard" />
  </form>
</template>
