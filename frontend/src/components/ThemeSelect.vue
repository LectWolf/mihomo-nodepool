<script setup lang="ts">
import CustomSelect, { type SelectOption } from './CustomSelect.vue'
import { setThemePreference, themePreference, type ThemePreference } from '../theme'
const options: SelectOption<ThemePreference>[] = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色模式' },
  { value: 'dark', label: '深色模式' },
]
</script>

<template>
  <div class="theme-control"><CustomSelect :model-value="themePreference" :options="options" label="外观模式" @update:model-value="setThemePreference" /></div>
</template>
