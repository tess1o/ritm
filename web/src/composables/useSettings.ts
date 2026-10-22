import { ref } from 'vue'
import { api } from '../lib/api'
import { setTimeZone } from '../lib/fmt'
import type { Settings, SettingsResponse } from '../lib/types'

const data = ref<SettingsResponse | null>(null)
let inflight: Promise<SettingsResponse> | null = null

export function useSettings() {
  async function load(force = false): Promise<SettingsResponse> {
    if (data.value && !force) return data.value
    inflight ??= api<SettingsResponse>('/settings').finally(() => (inflight = null))
    data.value = await inflight
    return data.value
  }
  async function save(s: Settings) {
    data.value = await api<SettingsResponse>('/settings', { method: 'PUT', body: s })
    setTimeZone(data.value.settings.timezone || data.value.server.timezone)
    return data.value
  }
  return { data, load, save }
}
