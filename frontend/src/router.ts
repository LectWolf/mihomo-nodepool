import { reactive, readonly, shallowRef } from 'vue'
import { classic } from './layout'

export type Section = 'catalog' | 'nodes' | 'sources' | 'settings' | 'statistics' | 'providers' | 'subscriptions' | 'rules' | 'users' | 'account'
interface AppRoute { section: Section; path: string; query: string }
const paths: Record<Section, string> = { catalog: '/catalog', nodes: '/nodes', sources: '/sources', settings: '/settings', statistics: '/statistics', providers: '/providers', subscriptions: '/subscriptions', rules: '/rules', users: '/users', account: '/account' }
const titles: Record<Section, string> = { catalog: '来源目录', nodes: '节点总览', sources: '共享来源', settings: '运行设置', statistics: '运行统计', providers: '提供方', subscriptions: '我的订阅', rules: '规则组合', users: '用户管理', account: '我的账号' }
const remembered = reactive(new Map<Section, string>())
function readLocation(): AppRoute {
  const path = location.pathname.replace(/\/+$/, '')
  // 根路径：经典版以来源目录为首页
  const section = (Object.keys(paths) as Section[]).find(key => paths[key] === path) ?? (classic.value ? 'catalog' : 'nodes')
  return { section, path: paths[section], query: location.search.slice(1) }
}
const state = shallowRef<AppRoute>(readLocation())
export const route = readonly(state)
function currentURL(value: AppRoute) { return value.path + (value.query ? `?${value.query}` : '') }
function publish(value: AppRoute) {
  remembered.set(value.section, currentURL(value))
  state.value = value
  document.title = `${titles[value.section]} · MCII节点共享`
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
export function navigate(section: Section, params?: URLSearchParams, replace = false) {
  const destination = params ? paths[section] + (params.size ? `?${params}` : '') : sectionHref(section)
  if (destination === currentURL(state.value)) return
  if (replace) history.replaceState(history.state, '', destination)
  else history.pushState(history.state, '', destination)
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
