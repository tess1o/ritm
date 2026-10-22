<script setup lang="ts">
import { ref } from 'vue'
import Card from '../../components/Card.vue'
import Icon from '../../components/Icon.vue'
import { useAuth } from '../../composables/useAuth'
import { useToast } from '../../composables/useToast'
import DangerModal from './DangerModal.vue'

const auth = useAuth()
const toast = useToast()
const pw = ref({ current: '', next: '', confirm: '' })
const pwBusy = ref(false)
async function changePassword() {
  if (pw.value.next !== pw.value.confirm) {
    toast.show('New passwords do not match', 'error')
    return
  }
  pwBusy.value = true
  try {
    await auth.changePassword(pw.value.current, pw.value.next)
    pw.value = { current: '', next: '', confirm: '' }
    toast.show('Password changed — your other sessions were signed out', 'success')
  } catch (e) {
    toast.show((e as Error).message, 'error', 6000)
  } finally {
    pwBusy.value = false
  }
}
const deleting = ref(false)
</script>

<template>
  <Card title="Password" :subtitle="`Signed in as ${auth.user.value?.username ?? ''}. Changing it signs out your other sessions.`">
    <form class="pwform" @submit.prevent="changePassword">
      <label class="field"><span>Current password</span>
        <input v-model="pw.current" class="input" type="password" autocomplete="current-password" required /></label>
      <div class="form-grid">
        <label class="field"><span>New password</span>
          <input v-model="pw.next" class="input" type="password" autocomplete="new-password" minlength="8" maxlength="72" required /></label>
        <label class="field"><span>Repeat new password</span>
          <input v-model="pw.confirm" class="input" type="password" autocomplete="new-password" minlength="8" maxlength="72" required /></label>
      </div>
      <button class="btn" type="submit" :disabled="pwBusy">{{ pwBusy ? 'Changing…' : 'Change password' }}</button>
    </form>
  </Card>

  <Card title="Danger zone">
    <div class="danger-row">
      <div>
        <strong>Delete account</strong>
        <p class="muted small">Removes your account, API keys and all data. This cannot be undone.</p>
      </div>
      <button class="btn danger" type="button" @click="deleting = true"><Icon name="trash" :size="16" /> Delete account</button>
    </div>
  </Card>

  <DangerModal v-if="deleting" kind="delete" @close="deleting = false" />
</template>

<style scoped>
.pwform {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 560px;
}
.pwform .btn {
  align-self: flex-start;
}
</style>
