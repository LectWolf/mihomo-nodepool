<script setup lang="ts">
import { computed, ref } from 'vue'
import { draftNodes, draftSources, toggleNode, toggleSource } from '../api/accounts'
import AppIcon from './AppIcon.vue'
import NodePreviewCards from './NodePreviewCards.vue'
import NodeBadges from './NodeBadges.vue'
import type { Source } from '../api/system'
import type { DetailedSelection } from '../community'
import '../community.css'
defineProps<{ disabled?: boolean }>()
const removed = ref('')
type WholeSource = typeof draftSources.value[number] & Pick<Source, 'min_level' | 'subscription_usage'>
const groups = computed(() => {
  const names = new Map<number, string>()
  for (const source of draftSources.value) names.set(source.id, source.name)
  for (const node of draftNodes.value) if (node.source_name) names.set(node.source_id, node.source_name)
  return [...new Set([...draftSources.value.map(s => s.id), ...draftNodes.value.map(n => n.source_id)])].map(id => ({ id, name: names.get(id) || `来源 #${id}`, whole: draftSources.value.find(s => s.id === id) as WholeSource | undefined, nodes: draftNodes.value.filter(n => n.source_id === id) as DetailedSelection[] }))
})
function removeNode(node: typeof draftNodes.value[number]) { toggleNode(node); removed.value = `已移除 ${node.name || '节点'}` }
function removeSource(source: typeof draftSources.value[number]) { toggleSource(source); removed.value = `已取消整来源 ${source.name}` }
</script>
<template>
  <div class="draft-preview">
    <div class="draft-heading"><h3>当前选择</h3><span class="small muted">{{ draftNodes.length }} 节点 · {{ draftSources.length }} 整来源</span></div>
    <p v-if="!draftNodes.length && !draftSources.length" class="small muted">尚未选择节点或来源。</p>
    <div v-else class="draft-groups">
      <section v-for="group in groups" :key="group.id" class="draft-source-group">
        <div class="draft-source-heading"><AppIcon name="sources" /><strong :title="group.name">{{ group.name }}</strong><small>{{ group.whole ? '整来源' : `${group.nodes.length} 个节点` }}</small></div>
        <div v-if="group.whole" class="draft-entry whole-source">
          <span><strong>已选整个来源</strong><small>{{ group.whole.node_count }} 个节点，按剩余额度截取；保存后可查看全部节点兼容性</small><NodeBadges class="whole-source-metadata" :min-level="group.whole.min_level" :usage="group.whole.subscription_usage" /></span>
          <button type="button" class="remove-selection" :disabled="disabled" :aria-label="`移除来源 ${group.name}`" @click="removeSource(group.whole)"><AppIcon name="close" /><span>移除</span></button>
        </div>
        <NodePreviewCards v-if="group.nodes.length" :nodes="group.nodes" removable :disabled="disabled" @remove="removeNode" />
      </section>
    </div>
    <span class="sr-only" role="status">{{ removed }}</span>
  </div>
</template>
