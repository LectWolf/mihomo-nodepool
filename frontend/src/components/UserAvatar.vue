<script setup lang="ts">
import { computed, ref, watch } from 'vue'
const props = withDefaults(defineProps<{ id?: number; name?: string; version?: number; size?: number }>(), { id: 0, name: '', version: 0, size: 32 })
const failed = ref(false)
watch(() => [props.id, props.version], () => { failed.value = false })
const initial = computed(() => (props.name.trim() || '?').slice(0, 1).toUpperCase())
// 按名称散列出稳定色相，没有头像时也能区分用户
const hue = computed(() => { let h = 0; for (const c of props.name || String(props.id)) h = (h * 31 + c.charCodeAt(0)) % 360; return h })
const src = computed(() => props.id && props.version > 0 && !failed.value ? `/api/v1/avatars/${props.id}?v=${props.version}` : '')
</script>
<template>
  <span class="user-avatar" :style="{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.42)}px`, '--avatar-hue': hue }" aria-hidden="true">
    <img v-if="src" :src="src" alt="" loading="lazy" @error="failed = true" />
    <template v-else>{{ initial }}</template>
  </span>
</template>
