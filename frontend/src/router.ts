import { reactive, readonly, shallowRef } from 'vue'

export type Section = 'nodes' | 'sources' | 'settings' | 'providers' | 'subscriptions' | 'rules' | 'users' | 'account'
interface AppRoute { section: Section; path: string; query: string }
const paths: Record<Section, string> = { nodes: '/nodes', sources: '/sources', settings: '/settings', providers: '/providers', subscriptions: '/subscriptions', rules: '/rules', users: '/users', account: '/account' }
const titles: Record<Section, string> = { nodes: '节点总览', sources: '我的来源', settings: '运行设置', providers: '提供方', subscriptions: '我的订阅', rules: '规则组合', users: '用户管理', account: '我的账号' }
const remembered = reactive(new Map<Section, string>())
function readLocation(): AppRoute {
  const path = location.pathname.replace(/\/+$/, '')
  const section = (Object.keys(paths) as Section[]).find(key => paths[key] === path) ?? 'nodes'
  return { section, path: paths[section], query: location.search.slice(1) }
}
const state = shallowRef<AppRoute>(readLocation())
export const route = readonly(state)
function currentURL(value: AppRoute) { return value.path + (value.query ? `?${value.query}` : '') }
function publish(value: AppRoute) {
  remembered.set(value.section, currentURL(value))
  state.value = value
  document.title = `${titles[value.section]} · NodePool`
}
function normalizeLocation(value: AppRoute) {
  const normalized = currentURL(value)
  if (location.pathname + location.search + location.hash !== normalized) history.replaceState(history.state, '', normalized)
}
normalizeLocation(state.value)
publish(state.value)
function popstate() {
  const next = readLocation()
  normalizeLocation(next)
  publish(next)
}
window.addEventListener('popstate', popstate)

export function sectionHref(section: Section) { return remembered.get(section) ?? paths[section] }
export function navigate(section: Section, params?: URLSearchParams) {
  const destination = params ? paths[section] + (params.size ? `?${params}` : '') : sectionHref(section)
  if (destination === currentURL(state.value)) return
  history.pushState(history.state, '', destination)
  publish(readLocation())
}
export function routeParams(section: Section) {
  const url = remembered.get(section) ?? paths[section]
  return new URLSearchParams(section === state.value.section ? state.value.query : url.split('?')[1] ?? '')
}
export function setRouteQuery(section: Section, params: URLSearchParams, replace = false) {
  if (state.value.section !== section) return
  const next = { section, path: paths[section], query: params.toString() }
  if (currentURL(next) === currentURL(state.value)) return
  if (replace) history.replaceState(history.state, '', currentURL(next))
  else history.pushState(history.state, '', currentURL(next))
  publish(next)
}
if (import.meta.hot) import.meta.hot.dispose(() => window.removeEventListener('popstate', popstate))
