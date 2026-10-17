<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { RuleGroup } from '../api/accounts'
import { errorText, isCancelled, useApi, type NodePage, type Options, type PoolNode } from '../api/system'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import CountryFilter from './CountryFilter.vue'
import Modal from './Modal.vue'

const props = defineProps<{ modelValue: RuleGroup[]; busy: boolean; readonly?: boolean; ruleTargets: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: RuleGroup[]]; rename: [oldName: string, name: string] }>()
const api = useApi()
const clone = (g: RuleGroup): RuleGroup => ({ ...g, node_ids: [...g.node_ids], countries: [...g.countries], groups: [...g.groups] })
const kinds: { value: RuleGroup['type']; label: string }[] = [{ value: 'select', label: '手动选择' }, { value: 'url-test', label: '自动选择' }, { value: 'fallback', label: '故障转移' }, { value: 'load-balance', label: '负载均衡' }]
const kindLabel = (kind: string) => kinds.find(v => v.value === kind)?.label || kind
const regionNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
const countryName = (code: string) => { try { return regionNames.of(code) || code } catch { return code } }
const draft = ref<RuleGroup | null>(null)
const editing = ref(-1)
const error = ref('')
const discardOpen = ref(false)
const initialDraft = ref('')
const nodeSearch = ref('')
const nodePage = ref(1)
const nodes = ref<PoolNode[]>([])
const totalNodes = ref(0)
const nodeLoading = ref(false)
const nodeError = ref('')
const optionsError = ref('')
const metadata = ref(new Map<number, PoolNode>())
const availableCountries = ref<Options['countries']>([])
const countryOptions = computed(() => {
  const values = new Map(availableCountries.value.filter(v => v.code !== 'CF').map(v => [v.code, v]))
  for (const code of ['HK', 'TW', 'MO', 'CN', 'JP', 'SG', 'US', ...(draft.value?.countries ?? [])]) if (code !== 'CF') values.set(code, { code, name: countryName(code) })
  return [...values.values()]
})
const pages = computed(() => Math.max(1, Math.ceil(totalNodes.value / 20)))
const refs = computed(() => ['DIRECT', 'REJECT', ...props.modelValue.map(g => g.name).filter(name => name !== props.modelValue[editing.value]?.name)])
const changed = computed(() => !!draft.value && JSON.stringify(draft.value) !== initialDraft.value)
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let optionsLoaded = false

function uniqueName(base: string) { let name = base; for (let n = 2; props.modelValue.some(g => g.name === name); n++) name = `${base} ${n}`; return name }
function open(index = -1, country = false) {
  if (props.busy) return
  editing.value = index
  const existing = props.modelValue[index]
  draft.value = existing ? clone(existing) : { name: uniqueName(country ? '香港节点' : '新策略组'), type: 'select', include_all: false, node_ids: [], countries: country ? ['HK'] : [], groups: [], interval: 300, tolerance: 50 }
  initialDraft.value = JSON.stringify(draft.value)
  error.value = ''; discardOpen.value = false; nodeSearch.value = ''; nodePage.value = 1
  if (!props.readonly) { void loadNodes(); void loadCountries() }
}
function preventImplicitSave(event: KeyboardEvent) {
  if (event.target instanceof HTMLInputElement && !['checkbox', 'button', 'submit'].includes(event.target.type)) event.preventDefault()
}
function requestClose() { if (props.busy) return; if (!props.readonly && changed.value) discardOpen.value = true; else close() }
function close() { controller?.abort(); clearTimeout(timer); draft.value = null; discardOpen.value = false }
async function loadCountries() {
  if (optionsLoaded) return
  try { availableCountries.value = (await api<Options>('/options')).countries; optionsLoaded = true; optionsError.value = '' }
  catch (e) { if (!isCancelled(e)) optionsError.value = errorText(e) }
}
async function loadNodes() {
  controller?.abort()
  if (!draft.value || props.readonly) return
  const active = new AbortController(); controller = active
  nodeLoading.value = true; nodeError.value = ''
  try {
    const params = new URLSearchParams({ page: String(nodePage.value), page_size: '20', q: nodeSearch.value.trim() })
    const result = await api<NodePage>(`/nodes?${params}`, { signal: active.signal })
    if (active.signal.aborted) return
    nodes.value = result.items; totalNodes.value = result.total
    for (const n of result.items) metadata.value.set(n.id, n)
  } catch (e) { if (!isCancelled(e)) nodeError.value = errorText(e) }
  finally { if (controller === active) nodeLoading.value = false }
}
watch(nodeSearch, () => { nodePage.value = 1; clearTimeout(timer); timer = setTimeout(() => { void loadNodes() }, 250) })
watch(nodePage, () => { void loadNodes() })
onBeforeUnmount(() => { controller?.abort(); clearTimeout(timer) })
function toggleNode(id: number) {
  const g = draft.value; if (!g || props.readonly || props.busy) return
  if (g.node_ids.includes(id)) g.node_ids = g.node_ids.filter(n => n !== id)
  else if (g.node_ids.length < 2000) g.node_ids.push(id)
}
function toggleRef(name: string) {
  const g = draft.value; if (!g || props.readonly || props.busy) return
  if (g.groups.includes(name)) g.groups = g.groups.filter(n => n !== name)
  else if (g.groups.length < 64) g.groups.push(name)
}
function removalReason(name: string) {
  if (name === 'NodePool') return 'NodePool 是主组，必须保留。'
  const referrer = props.modelValue.find(g => g.name !== name && g.groups.includes(name))
  if (referrer) return `请先从「${referrer.name}」中移除此组引用。`
  if (props.ruleTargets.includes(name)) return '请先修改指向此组的分流规则。'
  return ''
}
function remove(index: number) {
  const g = props.modelValue[index]
  if (!g || props.busy || props.readonly || removalReason(g.name)) return
  emit('update:modelValue', props.modelValue.filter((_, i) => i !== index).map(clone))
}
function apply() {
  if (!draft.value || props.busy || props.readonly) return
  const g = clone(draft.value); g.name = g.name.trim(); error.value = ''
  if (!g.name || [...g.name].length > 100 || /[\p{Cc},]/u.test(g.name) || ['DIRECT', 'REJECT', 'GLOBAL'].includes(g.name)) { error.value = '名称须为 1–100 字符，不能含控制字符、逗号或使用保留名称。'; return }
  if (props.modelValue.some((v, i) => i !== editing.value && v.name === g.name)) { error.value = '此策略组名称已存在。'; return }
  if (!Number.isInteger(g.interval) || g.interval < 60 || g.interval > 86400 || !Number.isInteger(g.tolerance) || g.tolerance < 0 || g.tolerance > 1000) { error.value = '检测间隔须为 60–86400 秒，容差须为 0–1000 毫秒。'; return }
  if (g.node_ids.length > 2000 || g.countries.length > 64 || g.groups.length > 64) { error.value = '每组最多 2000 个节点、64 个国家 / 地区和 64 个组引用。'; return }
  const next = props.modelValue.map(clone)
  const oldName = next[editing.value]?.name
  if (oldName) next[editing.value] = g; else next.push(g)
  if (oldName && oldName !== g.name) for (const group of next) group.groups = group.groups.map(name => name === oldName ? g.name : name)
  const definitions = new Map(next.map(group => [group.name, group]))
  const active = new Set<string>(); const done = new Set<string>()
  function visit(name: string): boolean {
    if (name === 'DIRECT' || name === 'REJECT' || done.has(name)) return true
    const group = definitions.get(name)
    if (!group || active.has(name)) return false
    active.add(name)
    if (!group.groups.every(visit)) return false
    active.delete(name); done.add(name); return true
  }
  if (!next.every(group => visit(group.name))) { error.value = '组引用不能悬空、自引用或形成循环。请调整「其他策略组」成员。'; return }
  emit('update:modelValue', next)
  if (oldName && oldName !== g.name) emit('rename', oldName, g.name)
  close()
}
</script>

<template>
  <section class="rule-groups-section">
    <div class="workbench-section-heading"><div><h3>策略组与成员</h3><p>组合本次订阅的节点、国家 / 地区与其他策略组。</p></div><span class="small muted">{{ modelValue.length }} / 64 组</span></div>
    <p class="notice info small">先添加国家组，再编辑「自动选择」或「故障转移」，勾选该国家组作为成员。若只想使用这些组，请关闭「包含本次订阅全部节点」。</p>
    <p class="small muted">按节点 ID 和出口国家匹配，仅输出本次订阅已选中的节点。没有可用成员的组安全回退到 DIRECT（直连）。HK / TW / MO 独立，CF 不作为国家。</p>
    <div v-if="!readonly" class="rule-group-toolbar"><button type="button" :disabled="busy || modelValue.length >= 64" @click="open()"><AppIcon name="plus" />新增策略组</button><button type="button" :disabled="busy || modelValue.length >= 64" @click="open(-1, true)"><AppIcon name="globe" />添加国家组</button></div>
    <div class="rule-group-list">
      <article v-for="(group, index) in modelValue" :key="group.name" class="rule-group-card">
        <div class="rule-group-card-heading"><strong>{{ group.name }}</strong><span class="rule-target">{{ kindLabel(group.type) }}</span></div>
        <p class="small muted">{{ group.include_all ? '本次订阅全部节点' : `${group.node_ids.length} 个指定节点` }}<template v-if="group.countries.length"> · {{ group.countries.map(countryName).join('、') }}</template></p>
        <p v-if="group.groups.length" class="rule-group-references small">引用：{{ group.groups.join(' · ') }}</p>
        <p v-if="!group.include_all && !group.node_ids.length && !group.countries.length && !group.groups.length" class="small muted">空组将回退到 DIRECT</p>
        <div class="rule-group-card-actions"><button type="button" class="compact" :disabled="busy" @click="open(index)">{{ readonly ? '查看成员' : '编辑组与成员' }}</button><button v-if="!readonly" type="button" class="text-button" :disabled="busy || !!removalReason(group.name)" :title="removalReason(group.name) || '移除此组'" @click="remove(index)">移除</button></div>
        <p v-if="!readonly && removalReason(group.name)" class="field-hint">{{ removalReason(group.name) }}</p>
      </article>
    </div>
    <Modal v-if="draft" :title="readonly ? `预览 ${draft.name}` : editing >= 0 ? '编辑策略组' : '新增策略组'" :busy="busy" wide @close="requestClose" @keydown.enter="preventImplicitSave">
      <div class="rule-group-dialog">
        <fieldset :disabled="busy || readonly" class="rule-group-fields">
          <div class="rule-group-basics"><label class="form-field"><span class="field-label">组名称</span><input v-model="draft.name" maxlength="100" :disabled="modelValue[editing]?.name === 'NodePool'" /><span v-if="modelValue[editing]?.name === 'NodePool'" class="field-hint">主组名称固定，成员和类型可编辑。</span></label><div class="form-field"><span class="field-label">策略类型</span><CustomSelect v-model="draft.type" label="策略组类型" :options="kinds" :disabled="busy || readonly" /></div></div>
          <div v-if="draft.type !== 'select'" class="rule-group-basics"><label class="form-field"><span class="field-label">检测间隔（秒）</span><input v-model.number="draft.interval" type="number" min="60" max="86400" step="1" /></label><label v-if="draft.type === 'url-test'" class="form-field"><span class="field-label">延迟容差（毫秒）</span><input v-model.number="draft.tolerance" type="number" min="0" max="1000" step="1" /></label><p class="field-hint">检测地址固定为 https://www.gstatic.com/generate_204</p></div>
          <label class="rule-group-check"><input v-model="draft.include_all" type="checkbox" /><span>包含本次订阅全部节点<small>关闭后使用指定节点与国家的并集；组引用始终追加。</small></span></label>
          <div class="form-field"><span class="field-label">国家 / 地区 <small class="muted">{{ draft.countries.length }} / 64</small></span><CountryFilter v-if="!readonly && !busy" v-model="draft.countries" :options="countryOptions" /><p v-else class="small">{{ draft.countries.map(countryName).join('、') || '未指定国家 / 地区' }}</p><span class="field-hint">未选择国家时不按国家添加节点。按出口 CountryCode 匹配；HK、TW、MO 各自独立。</span><span v-if="optionsError" class="small danger-text">地区列表加载失败：{{ optionsError }} <button type="button" class="text-button" @click="loadCountries">重试</button></span></div>
          <div class="form-field"><span class="field-label">其他策略组 <small class="muted">{{ draft.groups.length }} / 64</small></span><p class="field-hint">可将国家组加入自动选择 / 故障转移，支持嵌套，保存时检查循环引用。</p><div class="rule-group-ref-grid"><label v-for="name in refs" :key="name" class="rule-group-check"><input type="checkbox" :checked="draft.groups.includes(name)" :disabled="busy || readonly || (!draft.groups.includes(name) && draft.groups.length >= 64)" @change="toggleRef(name)" /><span>{{ name }}</span></label></div></div>
          <div class="form-field"><span class="field-label">指定节点 <small class="muted">{{ draft.node_ids.length }} / 2000 · 仅保存 ID</small></span><div v-if="draft.node_ids.length" class="rule-group-selected"><button v-for="id in draft.node_ids" :key="id" type="button" :disabled="busy || readonly" :aria-label="`移除节点 ${id}`" @click="toggleNode(id)">{{ metadata.get(id)?.name || `节点 #${id}` }}<AppIcon v-if="!readonly" name="close" /></button></div><p v-else-if="readonly" class="small muted">没有指定节点</p>
            <template v-if="!readonly"><label class="search-field"><AppIcon name="search" /><span class="sr-only">搜索组成员节点</span><input v-model="nodeSearch" type="search" placeholder="搜索节点名称或地址" /></label><p v-if="nodeError" role="alert" class="notice error">{{ nodeError }} <button type="button" @click="loadNodes">重试</button></p><p v-if="nodeLoading" class="small muted" role="status">正在加载节点…</p><div class="rule-group-node-list" :aria-busy="nodeLoading"><label v-for="node in nodes" :key="node.id" class="rule-group-check"><input type="checkbox" :checked="draft.node_ids.includes(node.id)" :disabled="busy || (!draft.node_ids.includes(node.id) && draft.node_ids.length >= 2000)" @change="toggleNode(node.id)" /><span><strong>{{ node.name }}</strong><small>#{{ node.id }} · {{ node.type }} · {{ node.country_code && node.country_code !== 'CF' ? countryName(node.country_code) : '地区未知' }} · {{ node.server }}:{{ node.port }}</small></span></label><p v-if="!nodes.length && !nodeLoading && !nodeError" class="small muted">没有匹配的节点</p></div><div class="compact-pagination"><span class="small muted">{{ totalNodes }} 个节点 · 每页 20 个</span><button type="button" :disabled="busy || nodeLoading || nodePage <= 1" @click="nodePage--">上一页</button><span>{{ nodePage }} / {{ pages }}</span><button type="button" :disabled="busy || nodeLoading || nodePage >= pages" @click="nodePage++">下一页</button></div></template>
          </div>
        </fieldset>
        <p class="notice info small">实际订阅中没有匹配成员时使用 DIRECT，避免输出悬空引用。此处选择不会把节点添加到订阅。</p>
        <p v-if="error" class="notice error" role="alert">{{ error }}</p>
        <div v-if="discardOpen" class="notice warning rule-group-discard"><span>放弃尚未应用的策略组修改？</span><button type="button" @click="discardOpen = false">继续编辑</button><button type="button" @click="close">放弃修改</button></div>
        <div class="dialog-actions"><button type="button" :disabled="busy" @click="requestClose">{{ readonly ? '关闭' : '取消' }}</button><button v-if="!readonly" type="button" class="primary" :disabled="busy" @click="apply">应用策略组</button></div>
      </div>
    </Modal>
  </section>
</template>
