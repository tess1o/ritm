import { ref } from 'vue'
import { api } from '../lib/api'
import { browserTimeZone, setTimeZone } from '../lib/fmt'
import type { Account, AuthConfig, Session, User } from '../lib/types'

const user = ref<User | null>(null)
const version = ref('')
const checked = ref(false)
const config = ref<AuthConfig | null>(null)

/** Forget everything the previous user left in this browser and start over on the login page. */
function leave() {
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith('ritm.')) localStorage.removeItem(k)
  } catch {
    /* storage unavailable */
  }
  // A full page load also drops in-memory state (period, import progress, cached settings).
  window.location.assign('/login')
}

export function useAuth() {
  async function load() {
    try {
      const r = await api<Account>('/account')
      setTimeZone(r.timezone)
      user.value = r.user
      version.value = r.version
    } catch {
      user.value = null
    } finally {
      checked.value = true
    }
  }
  async function loadConfig() {
    if (config.value) return config.value
    try {
      config.value = await api<AuthConfig>('/auth/config')
    } catch {
      config.value = { registration_enabled: false }
    }
    return config.value
  }
  async function login(username: string, password: string) {
    const r = await api<Session>('/auth/login', { method: 'POST', body: { username, password, timezone: browserTimeZone() } })
    setTimeZone(r.timezone)
    user.value = r.user
    checked.value = true
  }
  async function register(username: string, password: string) {
    const r = await api<Session>('/auth/register', { method: 'POST', body: { username, password, timezone: browserTimeZone() } })
    setTimeZone(r.timezone)
    user.value = r.user
    checked.value = true
  }
  async function logout() {
    try {
      await api('/auth/logout', { method: 'POST' })
    } finally {
      user.value = null
      leave()
    }
  }
  async function changePassword(current: string, next: string) {
    await api('/account/password', { method: 'PUT', body: { current_password: current, new_password: next } })
  }
  async function resetData(password: string) {
    await api('/account/reset', { method: 'POST', body: { password } })
    try {
      localStorage.removeItem('ritm.import.current')
    } catch {
      /* ignore */
    }
    window.location.assign('/import')
  }
  async function deleteAccount(password: string) {
    await api('/account', { method: 'DELETE', body: { password } })
    user.value = null
    leave()
  }
  return { user, version, checked, config, load, loadConfig, login, register, logout, changePassword, resetData, deleteAccount }
}
