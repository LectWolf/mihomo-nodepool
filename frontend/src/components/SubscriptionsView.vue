<script setup lang="ts">
import { toastRef } from '../toast'
import { reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { absoluteURL, copyText, currentUser, editSubscription, loadProfile, resetDraft, selectingNodes, type RuleProfile, type Subscription, limits } from '../api/accounts'
import { useAccountScope, type DetailedSelection } from '../community'
import { navigate } from '../router'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import ModeGuide from './ModeGuide.vue'
import RuleProfileSelect from './RuleProfileSelect.vue'
import LeaseCheckin from './LeaseCheckin.vue'
import NodePreviewCards from './NodePreviewCards.vue'
import SubscriptionQr from './SubscriptionQr.vue'
import SubscriptionHistory from './SubscriptionHistory.vue'
import SubscriptionArchives from './SubscriptionArchives.vue'
import ClientRecommendations from './ClientRecommendations.vue'
import FeedbackPanel from './FeedbackPanel.vue'
import UserAvatar from './UserAvatar.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const items = ref<Subscription[]>([])
const rules = ref<RuleProfile[]>([])
const loading = ref(false)
const error = ref('')
const message = toastRef()
const confirm = ref<{ item: Subscription; action: 'delete' | 'rotate' } | null>(null)
const details = ref<Subscription | null>(null)
const qr = ref<Subscription | null>(null)
const ruleTarget = ref<Subscription | null>(null)
const ruleID = ref<number | null>(null)
const busy = ref(false)
type ShownNode = DetailedSelection & { demoted?: boolean; provider_id?: number; provider_name?: string; provider_avatar?: number }
const feedbackNode = ref<ShownNode | null>(null)
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
  } else { ++sequence; loading.value = false; details.value = null; qr.value = null; ruleTarget.value = null; confirm.value = null; feedbackNode.value = null }
}, { immediate: true })
function create() { if (props.paused) return; resetDraft(); selectingNodes.value = true; navigate('nodes') }
function edit(item: Subscription) { editSubscription(item); details.value = null; navigate('nodes') }
function viewSource(id: number) { details.value = null; navigate('nodes', new URLSearchParams({ source_id: String(id) })) }
function ruleName(item: Subscription) { return item.rule_profile_id === 0 ? '官方默认规则' : item.rule_profile_id ? rules.value.find(rule => rule.id === item.rule_profile_id)?.name || `规则组合 #${item.rule_profile_id}` : '未绑定 · 仅节点配置' }
function displayNodes(item: Subscription): ShownNode[] {
  return (item.nodes ?? []).map(node => ({ ...node, ...(item.node_details as DetailedSelection[] | undefined)?.find(detail => detail.node_id === node.node_id && detail.source_id === node.source_id) }))
}
function uriCounts(item: Subscription) {
  const nodes = displayNodes(item).filter(node => node.available !== false)
  return { ok: nodes.filter(node => node.insights?.v2ray_compatible === true).length, skip: nodes.filter(node => node.insights?.v2ray_compatible === false).length }
}
// 订阅涉及的提供方，按节点数排序，用于卡片上的头像堆叠
function providers(item: Subscription) {
  const map = new Map<number, { id: number; name: string; avatar: number; count: number }>()
  for (const node of displayNodes(item)) {
    if (!node.provider_id) continue
    const p = map.get(node.provider_id) ?? { id: node.provider_id, name: node.provider_name || `用户 #${node.provider_id}`, avatar: node.provider_avatar ?? 0, count: 0 }
    p.count++; map.set(p.id, p)
  }
  for (const s of item.follow_details ?? []) {
    if (!s.provider_id) continue
    const p = map.get(s.provider_id) ?? { id: s.provider_id, name: s.provider_name || `用户 #${s.provider_id}`, avatar: s.provider_avatar ?? 0, count: 0 }
    p.count += s.available ? s.node_count : 0; map.set(p.id, p)
  }
  return [...map.values()].sort((a, b) => b.count - a.count)
}
function totalNodes(item: Subscription) { return item.node_count + (item.follow_node_count ?? 0) }
function follows(item: Subscription) { return item.follow_details ?? [] }
function demotedCount(item: Subscription) { return displayNodes(item).filter(node => node.demoted).length }
function unavailableCount(item: Subscription) { return displayNodes(item).filter(node => node.available === false).length }
function openFeedback(node: ShownNode) { feedbackNode.value = node }
function url(item: Subscription, format?: string) { const value = new URL(absoluteURL(item.url)); if (format) value.searchParams.set('format', format); return value.href }
// 订阅格式：记住上次选择，每张卡片可单独切换
type Format = 'clash' | 'auto' | 'v2ray'
const formatKey = 'nodepool-sub-format'
const stored = localStorage.getItem(formatKey)
const preferred = ref<Format>(stored === 'auto' || stored === 'v2ray' ? stored : 'clash')
const chosen = reactive<Record<number, Format>>({})
const formatOptions: { value: Format; label: string; icon: string; desc: string; badge?: string }[] = [
  { value: 'clash', label: 'Clash / Mihomo', icon: 'layers', desc: '完整配置，含规则', badge: '推荐' },
  { value: 'auto', label: '自动识别', icon: 'zap', desc: '按客户端返回' },
  { value: 'v2ray', label: 'v2ray URI', icon: 'list', desc: '仅节点，无规则' },
]
function formatOf(item: Subscription) { return chosen[item.id] ?? preferred.value }
function pick(item: Subscription, value: Format) { chosen[item.id] = value; preferred.value = value; localStorage.setItem(formatKey, value) }
function linkOf(item: Subscription) { const f = formatOf(item); return url(item, f === 'auto' ? undefined : f) }
function formatLabel(item: Subscription) { return formatOptions.find(f => f.value === formatOf(item))?.label ?? '' }
function formatHint(item: Subscription) {
  const f = formatOf(item)
  if (f === 'clash') return '完整的 Mihomo 配置，包含规则组合、策略组和按国家的分组，Clash Verge、FlClash、Mihomo Party 等客户端推荐使用。'
  if (f === 'auto') return '服务器根据客户端自动返回：Clash 系客户端得到完整配置，其他客户端得到 URI 列表。'
  const skip = uriCounts(item).skip
  return `Base64 编码的节点 URI，适合 v2rayN、Shadowrocket 等；规则组合不生效${skip ? `，会跳过 ${skip} 个不兼容节点` : ''}。`
}
function importLink(item: Subscription) { return `clash://install-config?url=${encodeURIComponent(url(item, 'clash'))}&name=${encodeURIComponent('MCII节点共享')}` }
function selectAll(event: Event) { (event.target as HTMLInputElement).select() }
function qrChanged(value: string) { if (qr.value && (value === 'clash' || value === 'auto' || value === 'v2ray')) pick(qr.value, value) }
async function copy(item: Subscription) {
  try { await copyText(linkOf(item)); message.value = `${formatLabel(item)} 订阅链接已复制。` }
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
const subscriptionGuide = [
  { icon: 'globe', tone: 'tone-blue', title: '节点来自共享', text: '订阅里的节点由提供方共享，提供方可以随时调整、停用或删除来源，平台无法保证某个节点一直可用，也不会为你保存节点。' },
  { icon: 'alert', tone: 'tone-red', title: '不要单独保存节点', text: '请始终在客户端里通过订阅链接更新，不要把单个节点复制、导出或另存到其他地方。节点地址只有提供方可见；短时间内频繁更换大量节点或跟随的来源会被风控限制（默认每天新增不超过对应额度，30 天内累计不超过额度的 3 倍），订阅链接被多个 IP 频繁拉取也会被标记。' },
  { icon: 'clock', tone: 'tone-amber', title: '席位需要续期', text: '组合订阅会占用所含来源的席位。按时签到，或让客户端保持定期更新和检测（订阅里隐藏的「席位自动续期」），即可自动续期；出门在外可用积分请假延期。过期后拉取会提示回网页激活。' },
  { icon: 'link', tone: 'tone-green', title: '链接只给自己用', text: '订阅链接包含你的个人令牌，请勿分享。怀疑泄露时，在订阅卡片中重置链接，旧链接会立即失效。' },
]
</script>
<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">MY SUBSCRIPTIONS</p><h1>我的订阅</h1><p class="muted">{{ items.length }} / {{ limits.subscriptions }} 条订阅 · 数量满额后可覆盖已有组合</p></div><button class="primary" :disabled="paused" @click="create"><AppIcon name="plus" />组合订阅</button></div>
    <ModeGuide storage-key="nodepool-guide-subscriptions" title="使用前请了解" summary="订阅里的节点来自其他用户的共享，会随时变化。请通过订阅链接使用，不要单独保存节点。" :items="subscriptionGuide" />
    <LeaseCheckin :paused="paused" @renewed="renewed" />
    <p v-if="error && !confirm && !ruleTarget" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>

    <div v-if="loading && !items.length" class="panel empty-state" role="status"><span class="spinner"></span><p>正在读取订阅…</p></div>
    <div v-else-if="!items.length && !error" class="panel empty-state"><AppIcon name="link" /><h3>创建你的第一条订阅</h3><p>进入选择模式，点击节点卡片或跟随整个来源，再保存组合。</p><button class="primary" @click="create">选择节点<AppIcon name="right" /></button></div>
    <article v-for="item in items" :key="item.id" class="panel sub-card">
      <header class="sub-head">
        <span class="sub-icon" aria-hidden="true"><AppIcon name="link" /></span>
        <div class="sub-title"><h2 :title="item.name">{{ item.name }}</h2><div class="sub-meta"><span><AppIcon name="nodes" />{{ totalNodes(item) }} 个节点</span><span><AppIcon name="sources" />{{ item.source_count }} 个来源</span><span v-if="follows(item).length" class="mini-tag blue" title="跟随的来源会自动同步节点，不占节点额度">跟随 {{ follows(item).length }} 个来源</span><span><AppIcon name="clock" />{{ formatDate(item.updated_at) }}</span></div></div>
        <div class="inline-actions"><button class="compact" @click="details = item"><AppIcon name="search" />查看节点</button><button class="compact" @click="edit(item)"><AppIcon name="edit" />编辑节点</button></div>
      </header>
      <div class="sub-body">
        <div class="sub-link">
          <span class="sub-label">导入到客户端</span>
          <div class="format-picker" role="radiogroup" :aria-label="`${item.name} 订阅格式`">
            <button v-for="f in formatOptions" :key="f.value" type="button" role="radio" :aria-checked="formatOf(item) === f.value" :class="{ active: formatOf(item) === f.value }" @click="pick(item, f.value)">
              <span class="format-icon"><AppIcon :name="f.icon" /></span>
              <span class="format-text"><strong>{{ f.label }}<em v-if="f.badge">{{ f.badge }}</em></strong><small>{{ f.desc }}</small></span>
            </button>
          </div>
          <div class="link-bar">
            <span class="link-bar-field"><AppIcon name="link" /><input :aria-label="`${item.name} ${formatLabel(item)} 订阅地址`" :value="linkOf(item)" readonly @focus="selectAll" /></span>
            <span class="link-bar-actions">
              <button type="button" class="primary compact" @click="copy(item)"><AppIcon name="copy" />复制链接</button>
              <a v-if="formatOf(item) !== 'v2ray'" class="button compact" :href="importLink(item)" title="在已安装的 Clash 系客户端中直接导入"><AppIcon name="download" />一键导入</a>
              <button type="button" class="compact" :aria-label="`${item.name} 二维码`" @click="qr = item"><AppIcon name="qr" />二维码</button>
            </span>
          </div>
          <p class="format-hint small"><AppIcon name="info" />{{ formatHint(item) }}</p>
        </div>
        <dl class="sub-facts">
          <div><dt>规则组合</dt><dd><strong :title="ruleName(item)">{{ ruleName(item) }}</strong><button class="text-button" @click="changeRule(item)">修改<AppIcon name="right" /></button></dd></div>
          <div v-if="follows(item).length"><dt>跟随来源</dt><dd class="follow-list"><button v-for="s in follows(item)" :key="s.source_id" type="button" :class="['mini-tag', s.available ? 'green' : 'red']" :title="s.available ? `${s.name}：当前 ${s.node_count} 个可用节点，自动同步` : `${s.name}：来源已停用或下架`" @click="viewSource(s.source_id)">{{ s.name }} · {{ s.available ? s.node_count : '不可用' }}</button></dd></div>
          <div v-if="item.node_count || !follows(item).length"><dt>URI 兼容</dt><dd><span class="mini-tag green">可导 {{ uriCounts(item).ok }}</span><span class="mini-tag">跳过 {{ uriCounts(item).skip }}</span><span v-if="unavailableCount(item)" class="mini-tag red">失效 {{ unavailableCount(item) }}</span></dd></div>
          <div class="sub-providers"><dt>提供方</dt><dd><span class="avatar-stack"><UserAvatar v-for="p in providers(item).slice(0, 5)" :key="p.id" :id="p.id" :name="p.name" :version="p.avatar" :size="24" /></span><span class="ellipsis small" :title="providers(item).map(p => `${p.name}（${p.count}）`).join('、')">{{ providers(item).length ? providers(item).slice(0, 2).map(p => p.name).join('、') + (providers(item).length > 2 ? ` 等 ${providers(item).length} 位` : '') : '暂无' }}</span></dd></div>
          <div v-if="demotedCount(item)"><dt>用户反馈</dt><dd class="amber-text"><AppIcon name="alert" />{{ demotedCount(item) }} 个节点被多人反馈，已排到末尾</dd></div>
        </dl>
      </div>
      <footer class="sub-foot"><span class="small muted"><AppIcon name="lock" />链接包含个人令牌，请勿分享</span><div><button class="text-button" @click="confirm = { item, action: 'rotate' }; error = ''"><AppIcon name="reset" />重置链接</button><button class="text-button danger" @click="confirm = { item, action: 'delete' }; error = ''"><AppIcon name="trash" />删除订阅</button></div></footer>
    </article>
    <SubscriptionArchives :paused="paused" :refresh-key="historyRevision" :subscriptions="items" :limit="limits.subscriptions" @restored="renewed" />
    <SubscriptionHistory :paused="paused" :refresh-key="historyRevision" />
    <ClientRecommendations />
    <Modal v-if="details" title="组合详情" wide @close="details = null">
      <h3 class="ellipsis" :title="details.name">{{ details.name }}</h3><p class="small muted">{{ totalNodes(details) }} 个节点 · {{ details.source_count }} 个来源 · {{ ruleName(details) }}</p>
      <div v-if="follows(details).length" class="follow-sources">
        <h4>跟随整个来源</h4>
        <button v-for="s in follows(details)" :key="s.source_id" type="button" class="follow-source" @click="viewSource(s.source_id)">
          <UserAvatar :id="s.provider_id" :name="s.provider_name" :version="s.provider_avatar" :size="28" />
          <span class="follow-source-text"><strong :title="s.name">{{ s.name }}</strong><small>{{ s.provider_name }} · {{ s.available ? `${s.node_count} 个可用节点，自动同步` : '来源已停用或下架' }}</small></span>
          <AppIcon name="right" />
        </button>
      </div>
      <NodePreviewCards v-if="details.nodes.length" :nodes="displayNodes(details)" grouped feedback @source="viewSource" @feedback="openFeedback" />
      <p v-if="details.nodes.length" class="small muted">按提供方分组的节点快照；客户端下载会过滤已失效、排除或停用的节点。节点有问题时点「反馈」告诉提供方。</p>
      <div class="dialog-actions"><button @click="details = null">关闭</button><button class="primary" @click="edit(details)"><AppIcon name="edit" />编辑组合</button></div>
    </Modal>
    <Modal v-if="feedbackNode" title="节点反馈" wide @close="feedbackNode = null">
      <h3 class="ellipsis" :title="feedbackNode.name">{{ feedbackNode.name || `节点 #${feedbackNode.node_id}` }}</h3><p class="small muted">{{ feedbackNode.source_name }}<template v-if="feedbackNode.provider_name"> · 提供方 {{ feedbackNode.provider_name }}</template></p>
      <FeedbackPanel :key="`${feedbackNode.node_id}-${feedbackNode.source_id}`" target="node" :node-id="feedbackNode.node_id" :origins="[{ id: feedbackNode.source_id, name: feedbackNode.source_name || `来源 #${feedbackNode.source_id}` }]" :can-submit="feedbackNode.provider_id !== currentUser?.id" start-open @changed="load" />
    </Modal>
    <SubscriptionQr v-if="qr && !paused" :url="qr.url" :title="`扫码导入 · ${qr.name}`" :format="formatOf(qr)" @update:format="qrChanged" @close="qr = null" />
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
