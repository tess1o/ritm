import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { setUnauthorizedHandler } from './lib/api'
import './styles/tokens.css'
import './styles/base.css'
import './styles/kpis.css'

setUnauthorizedHandler(() => {
  const current = router.currentRoute.value
  if (current.name !== 'login') router.replace({ name: 'login', query: { next: current.fullPath } })
})

createApp(App).use(router).mount('#app')

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  window.addEventListener('load', () => navigator.serviceWorker.register('/sw.js').catch(() => {}))
}
