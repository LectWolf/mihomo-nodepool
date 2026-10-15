<script lang="ts">
// Import SVG URLs only, without the package's CSS or emoji. Vite emits local assets.
const flags = import.meta.glob<string>('../../node_modules/flag-icons/flags/4x3/[a-z][a-z].svg', { eager: true, query: '?url&no-inline', import: 'default' })
const names = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(['zh-CN'], { type: 'region' }) : null
</script>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { PoolNode } from '../api/system'
const props = defineProps<{ node: Pick<PoolNode, 'country' | 'country_code' | 'exit_ip' | 'region' | 'city'> }>()
const code = computed(() => (props.node.country_code || '').trim().toUpperCase())
const flagUrl = computed(() => /^[A-Z]{2}$/.test(code.value) && code.value !== 'XX' ? flags[`../../node_modules/flag-icons/flags/4x3/${code.value.toLowerCase()}.svg`] : undefined)
const failed = ref(false)
watch(flagUrl, () => { failed.value = false })
const country = computed(() => {
  if (flagUrl.value && names) {
    const localized = names.of(code.value)
    if (localized && localized !== code.value) return localized
  }
  return (props.node.country || '').trim()
})
const location = computed(() => {
  const parts = [country.value, props.node.region, props.node.city].map(value => (value || '').trim()).filter(Boolean)
  return [...new Set(parts)].join(' · ') || '待识别'
})
</script>

<template>
  <div class="node-location">
    <div class="node-location-exit">
      <img v-if="flagUrl && !failed" :src="flagUrl" :alt="`${country || code}国旗`" class="country-flag" width="20" height="15" loading="lazy" @error="failed = true" />
      <svg v-else class="country-flag unknown-flag" viewBox="0 0 20 16" role="img" aria-label="国家待识别"><rect x=".5" y=".5" width="19" height="15" rx="2" fill="currentColor" opacity=".12" /><circle cx="10" cy="8" r="5" fill="none" stroke="currentColor" /><path d="M5 8h10M10 3c3 3 3 7 0 10-3-3-3-7 0-10Z" fill="none" stroke="currentColor" stroke-width=".8" /></svg>
      <span class="mono">{{ node.exit_ip?.trim() || '出口 IP 待识别' }}</span>
    </div>
    <span class="node-location-region">{{ location }}</span>
  </div>
</template>
