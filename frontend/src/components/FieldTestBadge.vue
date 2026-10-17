<script setup lang="ts">
import { computed } from 'vue'
import type { FieldTest } from '../api/system'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ result?: FieldTest | null; detail?: boolean }>()
const total = computed(() => (props.result?.ok ?? 0) + (props.result?.fail ?? 0))
const rate = computed(() => total.value ? (props.result!.ok / total.value) : 0)
const tone = computed(() => rate.value >= 0.7 ? 'good' : rate.value >= 0.3 ? 'warn' : 'bad')
const hint = computed(() => `近 3 天 ${total.value} 位国内用户的客户端实测：${props.result?.ok ?? 0} 位能连上，${props.result?.fail ?? 0} 位连不上`)
</script>
<template>
  <template v-if="total">
    <span v-if="!detail" :class="['field-test-badge', tone]" :title="hint" :aria-label="hint"><AppIcon :name="tone === 'bad' ? 'alert' : 'check'" />国内实测 {{ result!.ok }}/{{ total }}</span>
    <span v-else :class="['field-test-text', tone]">{{ result!.ok }}/{{ total }} 位国内用户能连上<small class="cell-sub">{{ hint }}。数据来自用户客户端的自动检测，比服务器检测更接近国内实际情况。</small></span>
  </template>
  <span v-else-if="detail" class="muted">暂无数据<small class="cell-sub">节点被国内用户加入订阅、客户端自动检测后才会有数据。</small></span>
</template>
