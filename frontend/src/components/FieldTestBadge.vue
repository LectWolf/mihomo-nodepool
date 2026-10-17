<script setup lang="ts">
import { computed } from 'vue'
import type { FieldTest } from '../api/system'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ result?: FieldTest | null; detail?: boolean }>()
const total = computed(() => (props.result?.ok ?? 0) + (props.result?.fail ?? 0))
const rate = computed(() => total.value ? (props.result!.ok / total.value) : 0)
const tone = computed(() => rate.value >= 0.7 ? 'good' : rate.value >= 0.3 ? 'warn' : 'bad')
const hint = computed(() => `近 ${props.result?.days ?? 3} 天 ${total.value} 位国内用户的客户端实测：${props.result?.ok ?? 0} 位能连上，${props.result?.fail ?? 0} 位连不上`)
const title = computed(() => hint.value + (props.result?.paused ? '；用户正在实测，本机暂停检测延迟' : ''))
</script>
<template>
  <template v-if="total">
    <span v-if="!detail" :class="['field-test-badge', tone]" :title="title" :aria-label="title"><AppIcon :name="tone === 'bad' ? 'alert' : 'check'" />国内实测 {{ result!.ok }}/{{ total }}</span>
    <span v-else :class="['field-test-text', tone]">{{ result!.ok }}/{{ total }} 位国内用户能连上<small class="cell-sub">{{ hint }}。数据来自用户客户端的自动检测，比服务器检测更接近国内实际情况，排序时优先参考。<template v-if="result!.paused">最近一次国内回报能连上，本机暂停检测（最长 24 小时），延迟为最后一次本机检测的结果；有人连不上或超过 24 小时没人回报后自动恢复。</template></small></span>
  </template>
  <span v-else-if="detail" class="muted">暂无数据<small class="cell-sub">节点被国内用户加入订阅、客户端自动检测后才会有数据。</small></span>
</template>
