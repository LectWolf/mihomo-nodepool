<script setup lang="ts">
import { draftNodes, draftSources, toggleNode, toggleSource } from '../api/accounts'
import AppIcon from './AppIcon.vue'
defineProps<{ disabled?: boolean }>()
</script>
<template>
  <div class="draft-preview">
    <h3>当前选择 <span class="count">{{ draftNodes.length }} 节点 · {{ draftSources.length }} 整来源</span></h3>
    <p v-if="!draftNodes.length && !draftSources.length" class="small muted">尚未选择节点或来源。</p>
    <ul v-else class="draft-list">
      <li v-for="source in draftSources" :key="`s${source.id}`"><span><strong>整来源 · {{ source.name }}</strong><small class="cell-sub muted">{{ source.node_count }} 个可用节点，保存时按剩余额度截取</small></span><button type="button" class="icon-button" :disabled="disabled" :aria-label="`移除来源 ${source.name}`" @click="toggleSource(source)"><AppIcon name="close" /></button></li>
      <li v-for="node in draftNodes" :key="node.node_id"><span>{{ node.name || `节点 #${node.node_id}` }} <small class="muted">{{ node.source_name || `来源 #${node.source_id}` }}</small></span><button type="button" class="icon-button" :disabled="disabled" :aria-label="`移除节点 ${node.name || node.node_id}`" @click="toggleNode(node)"><AppIcon name="close" /></button></li>
    </ul>
  </div>
</template>
