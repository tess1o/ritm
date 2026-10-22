import { ref } from 'vue'

export interface ToastMsg {
  id: number
  text: string
  kind: 'info' | 'error' | 'success'
}

const toasts = ref<ToastMsg[]>([])
let seq = 0

export function useToast() {
  function show(text: string, kind: ToastMsg['kind'] = 'info', ms = 3500) {
    const id = ++seq
    toasts.value = [...toasts.value, { id, text, kind }]
    setTimeout(() => (toasts.value = toasts.value.filter((t) => t.id !== id)), ms)
  }
  return { toasts, show }
}
