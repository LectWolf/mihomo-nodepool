<script setup lang="ts">
import { ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ storageKey: string; title: string; summary: string; items: { icon: string; title: string; text: string; tone?: string }[] }>()
// 记住用户的展开状态，首次进入默认展开
const read = () => { try { return localStorage.getItem(props.storageKey) !== 'closed' } catch { return true } }
const open = ref(read())
watch(open, value => { try { localStorage.setItem(props.storageKey, value ? 'open' : 'closed') } catch { /* 隐私模式下忽略 */ } })
</script>

<template>
  <section :class="['panel', 'mode-guide', { collapsed: !open }]">
    <button type="button" class="mode-guide-toggle" :aria-expanded="open" @click="open = !open">
      <span class="mode-guide-mark" aria-hidden="true"><AppIcon name="info" /></span>
      <span class="mode-guide-head"><strong>{{ title }}</strong><small>{{ summary }}</small></span>
      <span class="mode-guide-chevron"><span class="sr-only">{{ open ? '收起说明' : '展开说明' }}</span><AppIcon :name="open ? 'up' : 'down'" /></span>
    </button>
    <ol v-if="open" class="mode-guide-grid">
      <li v-for="item in items" :key="item.title" :class="item.tone || 'tone-blue'">
        <i class="metric-icon" aria-hidden="true"><AppIcon :name="item.icon" /></i>
        <div><strong>{{ item.title }}</strong><p>{{ item.text }}</p></div>
      </li>
    </ol>
  </section>
</template>
