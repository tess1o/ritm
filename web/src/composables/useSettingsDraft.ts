import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import type { Settings } from '../lib/types'
import { useFreshness } from './useFreshness'
import { useSettings } from './useSettings'
import { useToast } from './useToast'

/**
 * An editable copy of the settings for one Settings sub-page. The page saves only when something
 * changed (dirty), and leaving it with unsaved edits asks first.
 */
export function useSettingsDraft() {
  const { data, save } = useSettings()
  const toast = useToast()
  const fresh = useFreshness()

  const saved = computed(() => (data.value ? JSON.stringify(data.value.settings) : ''))
  const form = ref<Settings | null>(null)
  const discard = () => (form.value = saved.value ? JSON.parse(saved.value) : null)
  watch(saved, discard, { immediate: true })
  const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value)

  const saving = ref(false)
  async function submit() {
    if (!form.value || !dirty.value) return
    saving.value = true
    try {
      await save(form.value)
      fresh.load(true)
      toast.show('Settings saved — dashboards and insights recomputed', 'success')
    } catch (e) {
      toast.show((e as Error).message, 'error', 6000)
    } finally {
      saving.value = false
    }
  }

  onBeforeRouteLeave(() => !dirty.value || confirm('Discard unsaved changes?'))
  const onUnload = (e: BeforeUnloadEvent) => dirty.value && e.preventDefault()
  onMounted(() => window.addEventListener('beforeunload', onUnload))
  onUnmounted(() => window.removeEventListener('beforeunload', onUnload))

  return { form, res: computed(() => data.value?.resolved), server: computed(() => data.value?.server), dirty, saving, submit, discard }
}
