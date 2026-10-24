import { computed, ref, shallowRef, watch } from 'vue'
import { api, get } from '../lib/api'
import type { ApiKey, CreatedApiKey, Items, KeyScope } from '../lib/types'
import { useAsync } from './useAsync'
import { useToast } from './useToast'

// The last key list seen anywhere, so the app chrome knows whether an iPhone is connected.
const shared = shallowRef<ApiKey[] | null>(null)
let inflight: Promise<void> | null = null

/** API keys of the signed-in user: list, create (the secret is shown once) and revoke. */
export function useKeys() {
  const toast = useToast()
  const keys = useAsync((s) => get<Items<ApiKey>>('/api-keys', s))
  const created = ref<CreatedApiKey | null>(null)
  const creating = ref(false)
  watch(keys.data, (v) => v && (shared.value = v.items))

  async function create(name: string, scope: KeyScope): Promise<CreatedApiKey | null> {
    creating.value = true
    try {
      created.value = await api<CreatedApiKey>('/api-keys', { method: 'POST', body: { name, scope } })
      await keys.reload()
      return created.value
    } catch (e) {
      toast.show((e as Error).message, 'error')
      return null
    } finally {
      creating.value = false
    }
  }

  async function revoke(k: ApiKey) {
    if (!confirm(`Revoke key “${k.name}”? Clients using it stop working immediately.`)) return
    try {
      await api(`/api-keys/${k.id}`, { method: 'DELETE' })
      await keys.reload()
    } catch (e) {
      toast.show((e as Error).message, 'error')
    }
  }

  return { keys, created, creating, create, revoke }
}

/** Whether the iPhone Shortcut is connected: an active ingest key exists. `known` is false until the keys loaded. */
export function useIphoneShortcut() {
  function load(force = false) {
    if (shared.value && !force) return
    inflight ??= get<Items<ApiKey>>('/api-keys')
      .then((r) => void (shared.value = r.items), () => {})
      .finally(() => (inflight = null))
  }
  const active = computed(() => (shared.value ?? []).filter((k) => k.scope === 'ingest' && !k.revoked_at))
  const lastUsed = computed(() => active.value.map((k) => k.last_used_at).filter(Boolean).sort().at(-1) ?? null)
  return { load, known: computed(() => shared.value !== null), connected: computed(() => active.value.length > 0), active, lastUsed }
}

/** Copy text to the clipboard with a toast. */
export function useCopy() {
  const toast = useToast()
  return (text: string, what = 'Copied') =>
    navigator.clipboard?.writeText(text).then(() => toast.show(what), () => toast.show('Copy failed', 'error'))
}
