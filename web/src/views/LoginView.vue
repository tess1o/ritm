<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Icon from '../components/Icon.vue'
import { useAuth } from '../composables/useAuth'

const auth = useAuth()
const route = useRoute()
const router = useRouter()
const mode = ref<'signin' | 'register'>('signin')
const username = ref('')
const password = ref('')
const confirm = ref('')
const error = ref<string | null>(null)
const busy = ref(false)
const canRegister = computed(() => !!auth.config.value?.registration_enabled)

onMounted(() => auth.loadConfig())

function switchMode() {
  mode.value = mode.value === 'signin' ? 'register' : 'signin'
  error.value = null
  confirm.value = ''
}

async function submit() {
  error.value = null
  if (mode.value === 'register' && password.value !== confirm.value) {
    error.value = 'Passwords do not match'
    return
  }
  busy.value = true
  try {
    if (mode.value === 'register') await auth.register(username.value.trim(), password.value)
    else await auth.login(username.value.trim(), password.value)
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    router.replace(mode.value === 'register' ? '/import' : next)
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login">
    <form class="box" @submit.prevent="submit">
      <div class="brand">
        <span class="logo"><Icon name="activity" :size="22" /></span>
        <div>
          <h1>Ritm</h1>
          <p class="muted small">{{ mode === 'register' ? 'Create your account' : 'Your Apple Watch history' }}</p>
        </div>
      </div>
      <label class="field">
        <span>Username</span>
        <input v-model="username" class="input" autocomplete="username" autocapitalize="none" required autofocus />
      </label>
      <label class="field">
        <span>Password</span>
        <input v-model="password" class="input" type="password" :autocomplete="mode === 'register' ? 'new-password' : 'current-password'"
          :minlength="mode === 'register' ? 8 : undefined" required />
      </label>
      <label v-if="mode === 'register'" class="field">
        <span>Repeat password</span>
        <input v-model="confirm" class="input" type="password" autocomplete="new-password" minlength="8" required />
      </label>
      <p v-if="mode === 'register'" class="muted small">3–32 lowercase letters, digits, “.”, “_” or “-”. Password at least 8 characters.</p>
      <p v-if="error" class="error-box small" role="alert">{{ error }}</p>
      <button class="btn primary" type="submit" :disabled="busy">
        <template v-if="mode === 'register'">{{ busy ? 'Creating account…' : 'Create account' }}</template>
        <template v-else>{{ busy ? 'Signing in…' : 'Sign in' }}</template>
      </button>
      <p v-if="canRegister" class="switch small muted">
        {{ mode === 'register' ? 'Already have an account?' : 'New here?' }}
        <button class="link" type="button" @click="switchMode">{{ mode === 'register' ? 'Sign in' : 'Create an account' }}</button>
      </p>
    </form>
  </main>
</template>

<style scoped>
.login {
  min-height: 100dvh;
  display: grid;
  place-items: center;
  padding: calc(24px + var(--safe-top)) 16px calc(24px + var(--safe-bottom));
}
.box {
  width: 100%;
  max-width: 360px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 24px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 6px;
}
.logo {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 12px;
  background: var(--accent-bg);
  color: var(--accent);
}
.switch {
  margin: 0;
  text-align: center;
}
.link {
  border: 0;
  padding: 0;
  background: none;
  color: var(--accent);
  font: inherit;
  cursor: pointer;
}
.link:hover {
  text-decoration: underline;
}
</style>
