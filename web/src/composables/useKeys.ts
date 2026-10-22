import { ref } from 'vue'
import { api, get } from '../lib/api'
import type { ApiKey, CreatedApiKey, Items, KeyScope } from '../lib/types'
import { useAsync } from './useAsync'
import { useToast } from './useToast'

/** API keys of the signed-in user: list, create (the secret is shown once) and revoke. */
export function useKeys() {
  const toast = useToast()
  const keys = useAsync((s) => get<Items<ApiKey>>('/api-keys', s))
  const created = ref<CreatedApiKey | null>(null)
  const creating = ref(false)

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

/** Copy text to the clipboard with a toast. */
export function useCopy() {
  const toast = useToast()
  return (text: string, what = 'Copied') =>
    navigator.clipboard?.writeText(text).then(() => toast.show(what), () => toast.show('Copy failed', 'error'))
}
