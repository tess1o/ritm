<script setup lang="ts">
// Confirms a destructive action: both re-ask for the password; deleting the account also wants the username typed.
import { ref } from 'vue'
import Modal from '../../components/Modal.vue'
import { useAuth } from '../../composables/useAuth'

const props = defineProps<{ kind: 'reset' | 'delete' }>()
const emit = defineEmits<{ close: [] }>()
const auth = useAuth()
const password = ref('')
const name = ref('')
const busy = ref(false)
const error = ref<string | null>(null)

async function confirmDanger() {
  busy.value = true
  error.value = null
  try {
    if (props.kind === 'reset') await auth.resetData(password.value)
    else await auth.deleteAccount(password.value)
  } catch (e) {
    error.value = (e as Error).message
    busy.value = false
  }
}
</script>

<template>
  <Modal :title="kind === 'reset' ? 'Delete all data?' : 'Delete your account?'" @close="emit('close')">
    <form class="danger-form" @submit.prevent="confirmDanger">
      <p class="text-2">
        <template v-if="kind === 'reset'">All your health data, imports and settings will be permanently deleted. You stay signed in and can import again.</template>
        <template v-else>Your account <strong>{{ auth.user.value?.username }}</strong> and all its data will be permanently deleted.</template>
      </p>
      <label v-if="kind === 'delete'" class="field"><span>Type your username to confirm</span>
        <input v-model="name" class="input" autocomplete="off" autocapitalize="none" required /></label>
      <label class="field"><span>Password</span>
        <input v-model="password" class="input" type="password" autocomplete="current-password" required /></label>
      <p v-if="error" class="error-box small" role="alert">{{ error }}</p>
      <div class="row end">
        <button class="btn" type="button" @click="emit('close')">Cancel</button>
        <button class="btn danger" type="submit"
          :disabled="busy || !password || (kind === 'delete' && name.trim().toLowerCase() !== auth.user.value?.username)">
          {{ busy ? 'Deleting…' : kind === 'reset' ? 'Delete all data' : 'Delete account' }}
        </button>
      </div>
    </form>
  </Modal>
</template>

<style scoped>
.danger-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.danger-form p {
  margin: 0;
}
.end {
  justify-content: flex-end;
}
</style>
