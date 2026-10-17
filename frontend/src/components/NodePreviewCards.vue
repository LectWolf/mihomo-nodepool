<script setup lang="ts">
import { computed, ref } from 'vue'
import type { DetailedSelection } from '../community'
import AppIcon from './AppIcon.vue'
import NodeBadges from './NodeBadges.vue'
import Modal from './Modal.vue'
import TitleChip from './TitleChip.vue'
import UserAvatar from './UserAvatar.vue'
import '../community.css'
type Item = DetailedSelection & { demoted?: boolean; my_failed?: boolean; provider_id?: number; provider_name?: string; provider_avatar?: number; provider_title?: string }
const props = defineProps<{ nodes: Item[]; removable?: boolean; disabled?: boolean; grouped?: boolean; feedback?: boolean }>()
const emit = defineEmits<{ remove: [node: Item]; source: [id: number]; feedback: [node: Item] }>()
const detail = ref<Item | null>(null)
const counts = computed(() => {
  const available = props.nodes.filter(n => n.available !== false)
  return { compatible: available.filter(n => n.insights?.v2ray_compatible === true).length, skipped: available.filter(n => n.insights?.v2ray_compatible === false).length, unknown: available.filter(n => !n.insights).length, unavailable: props.nodes.length - available.length }
})
// 按提供方分组展示，组标题自带分隔，不再与节点卡片混排
const groups = computed(() => {
  if (!props.grouped) return [{ key: 'all', provider: null as Item | null, nodes: props.nodes }]
  const map = new Map<string, { key: string; provider: Item | null; nodes: Item[] }>()
  for (const node of props.nodes) {
    const key = node.provider_id ? `p${node.provider_id}` : 'unknown'
    if (!map.has(key)) map.set(key, { key, provider: node.provider_id ? node : null, nodes: [] })
    map.get(key)!.nodes.push(node)
  }
  return [...map.values()]
})
</script>
<template>
  <div class="preview-nodes">
    <div class="preview-toolbar"><p class="small muted">URI 可导 {{ counts.compatible }} · 跳过 {{ counts.skipped }}<span v-if="counts.unknown"> · 待分析 {{ counts.unknown }}</span><span v-if="counts.unavailable"> · 失效 {{ counts.unavailable }}</span></p></div>
    <section v-for="group in groups" :key="group.key" class="preview-group">
      <header v-if="grouped" class="preview-group-head">
        <template v-if="group.provider"><UserAvatar :id="group.provider.provider_id" :name="group.provider.provider_name" :version="group.provider.provider_avatar" :size="26" /><strong>{{ group.provider.provider_name }}</strong><TitleChip :name="group.provider.provider_title" /></template>
        <strong v-else>未知提供方</strong>
        <span class="small muted">{{ group.nodes.length }} 个节点</span>
      </header>
      <div class="preview-node-grid">
        <article v-for="node in group.nodes" :key="`${node.node_id}-${node.source_id}`" :class="['preview-node', { demoted: node.demoted || node.my_failed, unavailable: node.available === false }]">
          <div class="preview-node-title"><button type="button" class="text-button" :title="node.name || `节点 #${node.node_id}`" @click="detail = node">{{ node.name || `节点 #${node.node_id}` }}</button><span v-if="node.type" class="protocol-tag">{{ node.type.toUpperCase() }}</span></div>
          <span v-if="node.server" class="small muted mono ellipsis" :title="`${node.server}:${node.port}`">{{ node.server }}:{{ node.port }}</span>
          <span v-if="removable" class="small muted ellipsis" :title="node.source_name || `来源 #${node.source_id}`">{{ node.source_name || `来源 #${node.source_id}` }}</span>
          <button v-else type="button" class="text-button preview-source" :title="node.source_name || `来源 #${node.source_id}`" @click="emit('source', node.source_id)">{{ node.source_name || `来源 #${node.source_id}` }}<AppIcon name="right" /></button>
          <NodeBadges :insights="node.insights" :min-level="node.min_level" :usage="node.subscription_usage" />
          <span v-if="node.demoted" class="small amber-text"><AppIcon name="alert" />多人反馈异常，已排到订阅末尾</span><span v-else-if="node.my_failed" class="small amber-text" title="你的客户端自动检测时没能连上这个节点"><AppIcon name="alert" />你的客户端实测连不上，已排到订阅末尾</span>
          <span v-if="node.available === false" class="small danger-text">当前失效或停用，客户端下载会过滤</span>
          <button v-if="removable" type="button" class="remove-selection" :disabled="disabled" :aria-label="`移除节点 ${node.name || node.node_id}`" @click="emit('remove', node)"><AppIcon name="close" />移除</button>
          <button v-else-if="feedback" type="button" class="text-button preview-feedback" :aria-label="`反馈节点 ${node.name || node.node_id}`" @click="emit('feedback', node)"><AppIcon name="edit" />反馈</button>
        </article>
      </div>
    </section>
    <p v-if="counts.skipped" class="small muted">URI 会跳过无法保真导出的节点。推荐 Clash / Mihomo 完整配置；规则组合仅在 Clash 配置中生效。</p>
    <Modal v-if="detail" title="组合节点详情" @close="detail = null">
      <h3 :title="detail.name">{{ detail.name || `节点 #${detail.node_id}` }}</h3>
      <p class="small muted">{{ detail.source_name || `来源 #${detail.source_id}` }} · {{ detail.type?.toUpperCase() || '协议未知' }}<template v-if="detail.provider_name"> · 提供方 {{ detail.provider_name }}</template></p>
      <NodeBadges :insights="detail.insights" :min-level="detail.min_level" :usage="detail.subscription_usage" detail />
      <div class="dialog-actions"><button type="button" @click="detail = null">关闭</button><button v-if="feedback && !removable" type="button" class="primary" @click="emit('feedback', detail); detail = null"><AppIcon name="edit" />反馈这个节点</button></div>
    </Modal>
  </div>
</template>
