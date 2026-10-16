<script setup lang="ts">
import { computed, ref } from 'vue'
import type { DetailedSelection } from '../community'
import AppIcon from './AppIcon.vue'
import NodeBadges from './NodeBadges.vue'
import Modal from './Modal.vue'
import '../community.css'
const props = defineProps<{ nodes: DetailedSelection[]; removable?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ remove: [node: DetailedSelection]; source: [id: number] }>()
const layout = ref<'cards' | 'list'>('cards')
const detail = ref<DetailedSelection | null>(null)
const counts = computed(() => {
  const available = props.nodes.filter(n => n.available !== false)
  return { compatible: available.filter(n => n.insights?.v2ray_compatible === true).length, skipped: available.filter(n => n.insights?.v2ray_compatible === false).length, unknown: available.filter(n => !n.insights).length, unavailable: props.nodes.length - available.length }
})
</script>
<template>
  <div class="preview-nodes">
    <div class="preview-toolbar"><p class="small muted">URI 可导 {{ counts.compatible }} · 跳过 {{ counts.skipped }}<span v-if="counts.unknown"> · 待分析 {{ counts.unknown }}</span><span v-if="counts.unavailable"> · 失效 {{ counts.unavailable }}</span></p><div class="view-switch" role="group" aria-label="组合节点展示方式"><button type="button" :aria-pressed="layout === 'cards'" @click="layout = 'cards'">卡片</button><button type="button" :aria-pressed="layout === 'list'" @click="layout = 'list'">列表</button></div></div>
    <div :class="['preview-node-grid', { 'as-list': layout === 'list' }]">
      <article v-for="node in nodes" :key="`${node.node_id}-${node.source_id}`" class="preview-node">
        <div class="preview-node-title"><button type="button" class="text-button" :title="node.name || `节点 #${node.node_id}`" @click="detail = node">{{ node.name || `节点 #${node.node_id}` }}</button><span v-if="node.type" class="protocol-tag">{{ node.type.toUpperCase() }}</span></div>
        <span v-if="node.server" class="small muted mono ellipsis" :title="`${node.server}:${node.port}`">{{ node.server }}:{{ node.port }}</span>
        <span v-if="removable" class="small muted ellipsis" :title="node.source_name || `来源 #${node.source_id}`">{{ node.source_name || `来源 #${node.source_id}` }}</span>
        <button v-else type="button" class="text-button preview-source" :title="node.source_name || `来源 #${node.source_id}`" @click="emit('source', node.source_id)">{{ node.source_name || `来源 #${node.source_id}` }}<AppIcon name="right" /></button>
        <NodeBadges :insights="node.insights" :min-level="node.min_level" :usage="node.subscription_usage" />
        <span v-if="node.available === false" class="small danger-text">当前失效或停用，客户端下载会过滤</span>
        <button v-if="removable" type="button" class="remove-selection" :disabled="disabled" :aria-label="`移除节点 ${node.name || node.node_id}`" @click="emit('remove', node)"><AppIcon name="close" />移除</button>
      </article>
    </div>
    <p v-if="counts.skipped" class="small muted">URI 会跳过无法保真导出的节点。推荐 Clash / Mihomo 完整配置；规则组合仅在 Clash 配置中生效。</p>
    <Modal v-if="detail" title="组合节点详情" @close="detail = null">
      <h3 :title="detail.name">{{ detail.name || `节点 #${detail.node_id}` }}</h3>
      <p class="small muted">{{ detail.source_name || `来源 #${detail.source_id}` }} · {{ detail.type?.toUpperCase() || '协议未知' }}</p>
      <NodeBadges :insights="detail.insights" :min-level="detail.min_level" :usage="detail.subscription_usage" detail />
      <div class="dialog-actions"><button type="button" @click="detail = null">关闭</button></div>
    </Modal>
  </div>
</template>
