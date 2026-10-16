<script setup lang="ts">
import { ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { absoluteURL, copyText, currentUser, editSubscription, loadProfile, resetDraft, selectingNodes, type RuleProfile, type Subscription } from '../api/accounts'
import { useAccountScope, type DetailedSelection } from '../community'
import { navigate } from '../router'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import RuleProfileSelect from './RuleProfileSelect.vue'
import LeaseCheckin from './LeaseCheckin.vue'
import NodePreviewCards from './NodePreviewCards.vue'
import SubscriptionQr from './SubscriptionQr.vue'
import SubscriptionHistory from './SubscriptionHistory.vue'
import SubscriptionArchives from './SubscriptionArchives.vue'
import ClientRecommendations from './ClientRecommendations.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const items = ref<Subscription[]>([])
const rules = ref<RuleProfile[]>([])
const loading = ref(false)
const error = ref('')
const message = ref('')
const confirm = ref<{ item: Subscription; action: 'delete' | 'rotate' } | null>(null)
const details = ref<Subscription | null>(null)
const qr = ref<Subscription | null>(null)
const ruleTarget = ref<Subscription | null>(null)
const ruleID = ref<number | null>(null)
const busy = ref(false)
const historyRevision = ref(0)
let sequence = 0
async function load() {
  if (props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  loading.value = true
  try {
    const result = await Promise.all([api<{ items: Subscription[] }>('/subscriptions'), api<{ items: RuleProfile[] }>('/rule-profiles')])
    if (current !== sequence || !scope.current(ticket)) return
    items.value = result[0].items; rules.value = result[1].items; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch(() => props.paused, paused => {
  if (!paused) {
    void load()
    const ticket = scope.ticket()
    void loadProfile().catch(cause => { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) })
  } else { ++sequence; loading.value = false; details.value = null; qr.value = null; ruleTarget.value = null; confirm.value = null }
}, { immediate: true })
function create() { if (props.paused) return; resetDraft(); selectingNodes.value = true; navigate('nodes') }
function edit(item: Subscription) { editSubscription(item); details.value = null; navigate('nodes') }
function viewSource(id: number) { details.value = null; navigate('nodes', new URLSearchParams({ source_id: String(id) })) }
function ruleName(item: Subscription) { return item.rule_profile_id === 0 ? '官方默认规则' : item.rule_profile_id ? rules.value.find(rule => rule.id === item.rule_profile_id)?.name || `规则组合 #${item.rule_profile_id}` : '未绑定 · 仅节点配置' }
function displayNodes(item: Subscription): DetailedSelection[] {
  return (item.nodes ?? []).map(node => ({ ...node, ...(item.node_details as DetailedSelection[] | undefined)?.find(detail => detail.node_id === node.node_id && detail.source_id === node.source_id) }))
}
function uriSummary(item: Subscription) {
  const nodes = displayNodes(item).filter(node => node.available !== false)
  return `URI 可导 ${nodes.filter(node => node.insights?.v2ray_compatible === true).length} · 跳过 ${nodes.filter(node => node.insights?.v2ray_compatible === false).length}${nodes.some(node => !node.insights) ? ' · 部分节点待分析' : ''}`
}
function url(item: Subscription, format?: string) { const value = new URL(absoluteURL(item.url)); if (format) value.searchParams.set('format', format); return value.href }
async function copy(item: Subscription, format?: string) {
  try { await copyText(url(item, format)); message.value = `${format === 'clash' ? 'Clash ' : format === 'v2ray' ? 'v2ray ' : ''}订阅地址已复制。` }
  catch (cause) { error.value = errorText(cause) }
}
async function renewed() { historyRevision.value++; await load() }
async function perform() {
  const action = confirm.value
  if (!action || busy.value || props.paused) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''
  try {
    if (action.action === 'delete') await api(`/subscriptions/${action.item.id}`, json('DELETE'))
    else await api(`/subscriptions/${action.item.id}/rotate`, json('POST'))
    if (!scope.current(ticket)) return
    confirm.value = null; qr.value = null; details.value = null
    message.value = action.action === 'delete' ? '订阅已删除，原链接已失效；删除来源的最后引用会释放有效席位。' : '订阅令牌已重置，旧链接已失效。'
    historyRevision.value++; await load(); if (scope.current(ticket)) await loadProfile()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
function changeRule(item: Subscription) { ruleTarget.value = item; ruleID.value = item.rule_profile_id ?? null; error.value = '' }
async function saveRule() {
  const item = ruleTarget.value
  if (!item || busy.value || props.paused) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''
  try {
    await api(`/subscriptions/${item.id}`, json('PATCH', { rule_profile_id: ruleID.value }))
    if (!scope.current(ticket)) return
    ruleTarget.value = null; message.value = '订阅规则已更新，订阅链接保持不变。'; await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>
<template>
  <section>
    <div class="page-heading"><div><h1>我的订阅</h1><p class="muted">{{ items.length }} / {{ currentUser?.subscription_limit ?? 1 }} 条订阅 · 数量满额后可覆盖已有组合</p></div><button class="primary" :disabled="paused" @click="create"><AppIcon name="plus" />组合订阅</button></div>
    <LeaseCheckin :paused="paused" @renewed="renewed" />
    <p v-if="error && !confirm && !ruleTarget" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>
    <p v-if="message" role="status" class="notice success">{{ message }}</p>
    <div v-if="loading && !items.length" class="panel empty-state" role="status"><span class="spinner"></span><p>正在读取订阅…</p></div>
    <div v-else-if="!items.length && !error" class="panel empty-state"><AppIcon name="link" /><h3>创建你的第一条订阅</h3><p>进入选择模式，点击节点卡片或选取整个来源，再保存组合。</p><button class="primary" @click="create">选择节点<AppIcon name="right" /></button></div>
    <article v-for="item in items" :key="item.id" class="panel subscription-card">
      <div class="panel-heading"><div><h2 :title="item.name">{{ item.name }}</h2><p class="small muted">{{ item.node_count }} 个节点 · {{ item.source_count }} 个来源 · {{ formatDate(item.updated_at) }}</p><p class="small muted">{{ uriSummary(item) }}</p></div><div class="inline-actions"><button class="compact" @click="details = item">查看组合</button><button class="compact" @click="edit(item)"><AppIcon name="edit" />编辑节点</button></div></div>
      <div class="subscription-rule"><span class="small muted">规则组合</span><strong :title="ruleName(item)">{{ ruleName(item) }}</strong><button class="text-button" @click="changeRule(item)">修改规则<AppIcon name="right" /></button></div>
      <div class="copy-field"><input :aria-label="`${item.name} 订阅地址`" :value="url(item)" readonly /><button class="icon-button" :aria-label="`复制 ${item.name}`" title="复制订阅地址" @click="copy(item)"><AppIcon name="copy" /></button></div>
      <div class="subscription-formats"><button @click="copy(item)">自动识别</button><button @click="copy(item, 'clash')">Clash / Mihomo</button><button @click="copy(item, 'v2ray')">v2ray</button><button @click="qr = item">二维码</button><span class="small muted">链接包含访问令牌</span></div>
      <p class="small muted">Mihomo 完整配置首选；规则仅在 Clash 配置生效，v2ray URI 会跳过不兼容节点，且不保证全部参数兼容。</p>
      <div class="subscription-maintenance"><button class="text-button" @click="confirm = { item, action: 'rotate' }; error = ''"><AppIcon name="reset" />重置链接</button><button class="text-button danger" @click="confirm = { item, action: 'delete' }; error = ''"><AppIcon name="trash" />删除订阅</button></div>
    </article>
    <SubscriptionArchives :paused="paused" :refresh-key="historyRevision" />
    <SubscriptionHistory :paused="paused" :refresh-key="historyRevision" />
    <ClientRecommendations />
    <Modal v-if="details" title="组合详情" wide @close="details = null">
      <h3 class="ellipsis" :title="details.name">{{ details.name }}</h3><p class="small muted">{{ details.node_count }} 个节点 · {{ details.source_count }} 个来源 · {{ ruleName(details) }}</p>
      <NodePreviewCards :nodes="displayNodes(details)" @source="viewSource" />
      <p class="small muted">这里是保存的节点快照；客户端下载会过滤已失效、排除或停用的节点。删除某来源最后引用会释放席位。</p>
      <div class="dialog-actions"><button @click="details = null">关闭</button><button class="primary" @click="edit(details)"><AppIcon name="edit" />编辑组合</button></div>
    </Modal>
    <SubscriptionQr v-if="qr && !paused" :url="qr.url" :title="`${qr.name} · 订阅二维码`" @close="qr = null" />
    <Modal v-if="ruleTarget" title="修改订阅规则" :busy="busy" @close="ruleTarget = null">
      <p>为「{{ ruleTarget.name }}」选择规则组合。</p><RuleProfileSelect v-model="ruleID" :disabled="busy" /><p v-if="error" class="notice error" role="alert">{{ error }}</p>
      <div class="dialog-actions"><button :disabled="busy" @click="ruleTarget = null">取消</button><button class="primary" :disabled="busy" @click="saveRule">保存规则绑定</button></div>
    </Modal>
    <Modal v-if="confirm" :title="confirm.action === 'delete' ? '删除订阅' : '重置订阅链接'" :busy="busy" @close="confirm = null">
      <p>{{ confirm.action === 'delete' ? '删除后原订阅链接立即失效，删除来源的最后引用会释放有效席位。历史默认保留 30 天，以站点设置为准。' : '重置后旧订阅链接立即失效，需要在客户端替换为新地址。' }}</p><p v-if="error" class="notice error" role="alert">{{ error }}</p>
      <div class="dialog-actions"><button :disabled="busy" @click="confirm = null">取消</button><button class="danger-solid" :disabled="busy" @click="perform">确认{{ confirm.action === 'delete' ? '删除' : '重置' }}</button></div>
    </Modal>
  </section>
</template>
