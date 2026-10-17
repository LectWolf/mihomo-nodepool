<script setup lang="ts">
import { computed } from 'vue'
import { effectiveTheme, setThemePreference } from '../theme'
import AppIcon from './AppIcon.vue'
const dark = computed(() => effectiveTheme.value === 'dark')
function toggle() { setThemePreference(dark.value ? 'light' : 'dark') }
</script>
<template>
  <button type="button" class="theme-toggle icon-button" :aria-label="dark ? '切换到浅色模式' : '切换到深色模式'" :title="dark ? '切换到浅色模式' : '切换到深色模式'" :aria-pressed="dark" @click="toggle"><AppIcon :name="dark ? 'sun' : 'moon'" /></button>
</template>
