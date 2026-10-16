import { computed, readonly, ref, watchEffect } from 'vue'

export type ThemePreference = 'system' | 'light' | 'dark'
const key = 'nodepool-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')
function readPreference(): ThemePreference {
  try {
    const saved = localStorage.getItem(key)
    return saved === 'light' || saved === 'dark' ? saved : 'system'
  } catch { return 'system' }
}
const preference = ref<ThemePreference>(readPreference())
const systemDark = ref(media.matches)
export const themePreference = readonly(preference)
export const effectiveTheme = computed(() => preference.value === 'system' ? (systemDark.value ? 'dark' : 'light') : preference.value)
export function setThemePreference(value: ThemePreference) {
  preference.value = value
  try { localStorage.setItem(key, value) } catch { /* This tab can still switch when storage is unavailable. */ }
}
const stop = watchEffect(() => {
  document.documentElement.dataset.theme = effectiveTheme.value
  document.documentElement.style.colorScheme = effectiveTheme.value
  document.documentElement.style.backgroundColor = effectiveTheme.value === 'dark' ? '#12171c' : '#f5f7f6'
})
function systemChanged(event: MediaQueryListEvent) { systemDark.value = event.matches }
function storageChanged(event: StorageEvent) { if (event.key === key || event.key === null) preference.value = readPreference() }
media.addEventListener('change', systemChanged)
window.addEventListener('storage', storageChanged)
if (import.meta.hot) import.meta.hot.dispose(() => {
  stop()
  media.removeEventListener('change', systemChanged)
  window.removeEventListener('storage', storageChanged)
})
