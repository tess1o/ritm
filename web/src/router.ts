import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuth } from './composables/useAuth'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    title?: string
    period?: boolean // show the date presets in the top bar
    compare?: boolean // offer "compare to previous period"
    freshness?: boolean // show the data-freshness chip in the top bar on phones
    left?: boolean // align the page and its top bar to the left instead of centring them
  }
}

// Every screen is a lazy-loaded route (FE-3).
const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
  { path: '/', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: 'This week', freshness: true } },
  { path: '/activity', name: 'activity', component: () => import('./views/ActivityView.vue'), meta: { title: 'Activity', period: true, compare: true } },
  { path: '/day/:day?', name: 'day', component: () => import('./views/DayView.vue'), meta: { title: 'Day' } },
  { path: '/heart', name: 'heart', component: () => import('./views/HeartView.vue'), meta: { title: 'Heart', period: true, compare: true } },
  { path: '/training', name: 'training', component: () => import('./views/TrainingView.vue'), meta: { title: 'Training', period: true, compare: true } },
  { path: '/workouts/:id', name: 'workout', component: () => import('./views/WorkoutView.vue'), meta: { title: 'Workout' } },
  { path: '/sleep', name: 'sleep', component: () => import('./views/SleepView.vue'), meta: { title: 'Sleep & recovery', period: true, compare: true } },
  { path: '/insights', name: 'insights', component: () => import('./views/InsightsView.vue'), meta: { title: 'Insights', period: true } },
  { path: '/import', name: 'import', component: () => import('./views/ImportView.vue'), meta: { title: 'Import' } },
  // not /mcp: that path is the MCP endpoint itself
  { path: '/assistant', name: 'assistant', component: () => import('./views/AssistantView.vue'), meta: { title: 'AI assistants' } },
  {
    path: '/settings',
    component: () => import('./views/settings/SettingsLayout.vue'),
    meta: { title: 'Settings', left: true },
    children: [
      { path: '', name: 'settings', redirect: { name: 'settings-general' } },
      { path: 'general', name: 'settings-general', component: () => import('./views/settings/GeneralSettings.vue') },
      { path: 'heart', name: 'settings-heart', component: () => import('./views/settings/HeartSettings.vue') },
      { path: 'sources', name: 'settings-sources', component: () => import('./views/settings/SourcesSettings.vue') },
      { path: 'connections', name: 'settings-connections', component: () => import('./views/settings/ConnectionsSettings.vue') },
      { path: 'data', name: 'settings-data', component: () => import('./views/settings/DataSettings.vue') },
      { path: 'account', name: 'settings-account', component: () => import('./views/settings/AccountSettings.vue') },
    ],
  },
  { path: '/more', name: 'more', component: () => import('./views/MoreView.vue'), meta: { title: 'More' } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  const auth = useAuth()
  if (!auth.checked.value) await auth.load()
  if (!auth.user.value) return { name: 'login', query: { next: to.fullPath } }
  return true
})
