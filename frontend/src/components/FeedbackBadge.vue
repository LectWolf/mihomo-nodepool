<script setup lang="ts">
import { computed } from 'vue'
import type { FeedbackSummary } from '../community'
import { timeAgo } from '../community'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ summary?: FeedbackSummary | null }>()
const tone = computed(() => !props.summary ? '' : props.summary.bad > props.summary.ok ? 'bad' : props.summary.bad ? 'warn' : 'good')
const text = computed(() => {
  const s = props.summary
  if (!s) return ''
  return s.bad ? `${s.bad} 条问题反馈` : `${s.ok} 人反馈可用`
})
</script>
<template>
  <span v-if="summary && summary.total" :class="['feedback-badge', tone]" :title="`近期 ${summary.total} 条反馈，最近一条 ${timeAgo(summary.latest_at)}`"><AppIcon :name="summary.bad ? 'alert' : 'check'" />{{ text }}</span>
</template>
