<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Source } from '../api/system'
import { draftNodes, draftSources, editingSubscription, isGuest, reviewRequested, resetDraft, selectionError, selectionNotice } from '../api/accounts'
import { navigate } from '../router'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import LeaseCheckin from './LeaseCheckin.vue'
import SourceGallery from './SourceGallery.vue'
import ClassicSourceDialog from './ClassicSourceDialog.vue'
import ClassicSubscriptionBar from './ClassicSubscriptionBar.vue'
import '../community.css'
// 经典版首页：当前订阅 + 来源目录，点开来源查看节点
const props = defineProps<{ paused: boolean }>()
const viewing = ref<{ id: number; list: Source[] } | null>(null)
const checkinOpen = ref(false)
const bar = ref<InstanceType<typeof ClassicSubscriptionBar>>()
const hasDraft = computed(() => draftNodes.value.length > 0 || draftSources.value.length > 0 || editingSubscription.value !== null)
function view(source: Source, list: Source[]) { viewing.value = { id: source.id, list: list.length ? list : [source] } }
function save() { reviewRequested.value = true; navigate('nodes') }
watch(() => props.paused, paused => { if (paused) { viewing.value = null; checkinOpen.value = false } })
</script>

<template>
  <section class="classic-home">
    <div class="classic-heading"><div><h1>来源目录</h1><p class="small muted">点开来源查看节点，跟随整个来源或单独挑选节点组合订阅。</p></div><div class="classic-heading-actions"><button v-if="!isGuest" type="button" class="compact" @click="checkinOpen = true"><AppIcon name="check" />签到</button><button type="button" class="compact" @click="navigate('nodes')"><AppIcon name="nodes" />全部节点</button></div></div>
    <ClassicSubscriptionBar ref="bar" :paused="paused" @checkin="checkinOpen = true" />
    <div v-if="hasDraft" class="selection-bar classic-selection"><div><strong>{{ editingSubscription ? '编辑组合' : '已选' }}</strong><span>{{ draftNodes.length }} 个单选节点 · 跟随 {{ draftSources.length }} 个来源</span></div><button type="button" class="text-button" @click="resetDraft">清空</button><button type="button" class="primary" @click="save">{{ editingSubscription ? '保存修改' : '保存组合' }}<AppIcon name="right" /></button></div>
    <p v-if="selectionError" class="notice warning" role="alert">{{ selectionError }}</p>
    <p v-if="selectionNotice" class="notice info" role="status">{{ selectionNotice }}</p>
    <SourceGallery classic :paused="paused" @view="view" />
    <ClassicSourceDialog v-if="viewing && !paused" :list="viewing.list" :initial="viewing.id" :paused="paused" @close="viewing = null" />
    <Modal v-if="checkinOpen && !paused" title="签到与激活" @close="checkinOpen = false"><LeaseCheckin :paused="paused" @renewed="bar?.load()" /></Modal>
  </section>
</template>
