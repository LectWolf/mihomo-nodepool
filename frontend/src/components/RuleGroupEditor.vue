<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import type { RuleGroup } from '../api/accounts'
import { errorText, isCancelled, useApi, type Options } from '../api/system'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import CountryFilter from './CountryFilter.vue'
import Modal from './Modal.vue'
import { groupLabel, mainGroupLabel } from '../subscription-name'

const props = defineProps<{ modelValue: RuleGroup[]; busy: boolean; readonly?: boolean; ruleTargets: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: RuleGroup[]]; rename: [oldName: string, name: string] }>()
const api = useApi()
const clone = (g: RuleGroup): RuleGroup => ({ ...g, node_ids: [...(g.node_ids ?? [])], countries: [...g.countries], groups: [...g.groups], country_groups: g.country_groups !== false })
const kinds: { value: RuleGroup['type']; label: string }[] = [{ value: 'select', label: '手动选择' }, { value: 'url-test', label: '自动选择' }, { value: 'fallback', label: '故障转移' }, { value: 'load-balance', label: '负载均衡' }]
const kindLabel = (kind: string) => kinds.find(v => v.value === kind)?.label || kind
const regionNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
const countryName = (code: string) => { try { return regionNames.of(code) || code } catch { return code } }
const draft = ref<RuleGroup | null>(null)
const editing = ref(-1)
const error = ref('')
const discardOpen = ref(false)
const keepEditing = ref<HTMLButtonElement>()
// 确认条出现在吸底栏内，并把焦点移到安全选项
watch(discardOpen, async open => { if (open) { await nextTick(); keepEditing.value?.focus() } })
const initialDraft = ref('')
const optionsError = ref('')
const availableCountries = ref<Options['countries']>([])
const countryOptions = computed(() => {
  const values = new Map(availableCountries.value.filter(v => v.code !== 'CF').map(v => [v.code, v]))
  for (const code of ['HK', 'TW', 'MO', 'CN', 'JP', 'SG', 'US', ...(draft.value?.countries ?? [])]) if (code !== 'CF') values.set(code, { code, name: countryName(code) })
  return [...values.values()]
})
const flag = (code: string) => code.length === 2 ? String.fromCodePoint(...[...code.toUpperCase()].map(c => 0x1F1E6 + c.charCodeAt(0) - 65)) : ''
// 预览自动国家分组：以当前节点池里出现的国家为例，实际以订阅节点为准
const countryPreview = computed(() => availableCountries.value.filter(v => v.code !== 'CF').slice(0, 8).map(v => `${flag(v.code)} ${countryName(v.code)}`))
const mountsCountries = (g: RuleGroup) => g.type === 'select' && g.include_all && g.country_groups !== false
const rejectFirst = (g: RuleGroup) => !g.include_all && !g.countries.length && g.groups[0] === 'REJECT'
function members(g: RuleGroup) {
  const parts: string[] = []
  if (g.groups.length) parts.push(g.groups.map(groupLabel).join('、'))
  if (mountsCountries(g)) parts.push('各国家分组')
  if (g.include_all) parts.push('全部节点')
  else if (g.countries.length) parts.push(`${g.countries.map(countryName).join('、')}的节点`)
  return parts
}
const refs = computed(() => ['DIRECT', 'REJECT', ...props.modelValue.map(g => g.name).filter(name => name !== props.modelValue[editing.value]?.name)])
const changed = computed(() => !!draft.value && JSON.stringify(draft.value) !== initialDraft.value)
let optionsLoaded = false

function uniqueName(base: string) { let name = base; for (let n = 2; props.modelValue.some(g => g.name === name); n++) name = `${base} ${n}`; return name }
function open(index = -1, country = false) {
  if (props.busy) return
  editing.value = index
  const existing = props.modelValue[index]
  draft.value = existing ? clone(existing) : { name: uniqueName(country ? '港澳台' : '新策略组'), type: 'select', include_all: false, node_ids: [], countries: country ? ['HK', 'TW', 'MO'] : [], groups: [], interval: 300, tolerance: 50, country_groups: true }
  initialDraft.value = JSON.stringify(draft.value)
  error.value = ''; discardOpen.value = false
  void loadCountries()
}
function preventImplicitSave(event: KeyboardEvent) {
  if (event.target instanceof HTMLInputElement && !['checkbox', 'button', 'submit'].includes(event.target.type)) event.preventDefault()
}
function requestClose() { if (props.busy) return; if (!props.readonly && changed.value) discardOpen.value = true; else close() }
function close() { draft.value = null; discardOpen.value = false }
async function loadCountries() {
  if (optionsLoaded) return
  try { availableCountries.value = (await api<Options>('/options')).countries; optionsLoaded = true; optionsError.value = '' }
  catch (e) { if (!isCancelled(e)) optionsError.value = errorText(e) }
}
onMounted(() => { void loadCountries() })
function toggleRef(name: string) {
  const g = draft.value; if (!g || props.readonly || props.busy) return
  if (g.groups.includes(name)) g.groups = g.groups.filter(n => n !== name)
  else if (g.groups.length < 64) g.groups.push(name)
}
function removalReason(name: string) {
  if (name === 'NodePool') return `${mainGroupLabel} 是主组，必须保留。`
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
  const g = clone(draft.value); g.name = g.name.trim(); g.node_ids = []; error.value = ''
  if (!g.name || [...g.name].length > 100 || /[\p{Cc},]/u.test(g.name) || ['DIRECT', 'REJECT', 'GLOBAL', mainGroupLabel].includes(g.name)) { error.value = '名称须为 1–100 字符，不能含控制字符、逗号或使用保留名称。'; return }
  if (props.modelValue.some((v, i) => i !== editing.value && v.name === g.name)) { error.value = '此策略组名称已存在。'; return }
  if (!Number.isInteger(g.interval) || g.interval < 60 || g.interval > 86400 || !Number.isInteger(g.tolerance) || g.tolerance < 0 || g.tolerance > 1000) { error.value = '检测间隔须为 60–86400 秒，容差须为 0–1000 毫秒。'; return }
  if (g.countries.length > 64 || g.groups.length > 64) { error.value = '每组最多 64 个国家 / 地区和 64 个组引用。'; return }
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
    <div class="rule-group-explain">
      <p><AppIcon name="globe" /><span><b>自动国家分组</b>：订阅里有哪些国家的节点，就自动生成对应分组（如 🇭🇰 香港、🇯🇵 日本），挂到开启此项的手动选择组里，选一个国家即可自动用该国最快的节点。</span></p>
      <p><AppIcon name="layers" /><span><b>成员顺序</b>：引用的其他策略组 → 国家分组 → 节点。策略组只按“全部节点 / 国家 / 其他策略组”组合成员，不能单独指定节点，成员始终来自本次订阅已选的节点。</span></p>
      <p><AppIcon name="shield" /><span><b>拦截类分组</b>（如 🛑 拦截广告）也是可切换的手动选择组，只是第一个成员是 REJECT，所以默认拦截；遇到网站异常可在客户端里切到 DIRECT。</span></p>
    </div>
    <div v-if="!readonly" class="rule-group-toolbar"><button type="button" :disabled="busy || modelValue.length >= 64" @click="open()"><AppIcon name="plus" />新增策略组</button><button type="button" :disabled="busy || modelValue.length >= 64" title="例如把香港、台湾、澳门合成一个组" @click="open(-1, true)"><AppIcon name="globe" />添加多国组合组</button></div>
    <div class="rule-group-list">
      <article v-for="(group, index) in modelValue" :key="group.name" class="rule-group-card">
        <div class="rule-group-card-heading"><strong>{{ groupLabel(group.name) }}</strong><span class="rule-target">{{ kindLabel(group.type) }}</span></div>
        <ol v-if="members(group).length" class="rule-group-members small"><li v-for="(part, i) in members(group)" :key="i">{{ part }}</li></ol>
        <p v-else class="small muted">空组将回退到 DIRECT</p>
        <div class="rule-group-tags"><span v-if="mountsCountries(group)" class="mini-tag green"><AppIcon name="globe" />自动国家分组</span><span v-if="rejectFirst(group)" class="mini-tag red"><AppIcon name="shield" />默认拦截</span><span v-if="group.node_ids?.length" class="mini-tag amber" title="旧版本保存的指定节点，保存时会自动忽略">{{ group.node_ids.length }} 个旧版指定节点将忽略</span></div>
        <div class="rule-group-card-actions"><button type="button" class="compact" :disabled="busy" @click="open(index)">{{ readonly ? '查看成员' : '编辑组与成员' }}</button><button v-if="!readonly" type="button" class="text-button" :disabled="busy || !!removalReason(group.name)" :title="removalReason(group.name) || '移除此组'" @click="remove(index)">移除</button></div>
        <p v-if="!readonly && removalReason(group.name)" class="field-hint">{{ removalReason(group.name) }}</p>
      </article>
    </div>
    <Modal v-if="draft" :title="readonly ? `预览 ${groupLabel(draft.name)}` : editing >= 0 ? '编辑策略组' : '新增策略组'" :busy="busy" wide @close="requestClose" @keydown.enter="preventImplicitSave">
      <div class="rule-group-dialog">
        <fieldset :disabled="busy || readonly" class="rule-group-fields">
          <div class="rule-group-basics"><label class="form-field"><span class="field-label">组名称</span><input v-if="modelValue[editing]?.name === 'NodePool'" :value="mainGroupLabel" disabled /><input v-else v-model="draft.name" maxlength="100" /><span v-if="modelValue[editing]?.name === 'NodePool'" class="field-hint">主组名称固定，成员和类型可编辑。</span></label><div class="form-field"><span class="field-label">策略类型</span><CustomSelect v-model="draft.type" label="策略组类型" :options="kinds" :disabled="busy || readonly" /></div></div>
          <div v-if="draft.type !== 'select'" class="rule-group-basics"><label class="form-field"><span class="field-label">检测间隔（秒）</span><input v-model.number="draft.interval" type="number" min="60" max="86400" step="1" /></label><label v-if="draft.type === 'url-test'" class="form-field"><span class="field-label">延迟容差（毫秒）</span><input v-model.number="draft.tolerance" type="number" min="0" max="1000" step="1" /></label><p class="field-hint">检测地址固定为 https://www.gstatic.com/generate_204</p></div>
          <label class="rule-group-check"><input v-model="draft.include_all" type="checkbox" /><span>包含本次订阅全部节点<small>关闭后只包含下方所选国家的节点；引用的策略组始终排在节点前面。</small></span></label>
          <label v-if="draft.type === 'select' && draft.include_all" class="rule-group-check"><input v-model="draft.country_groups" type="checkbox" /><span>挂载自动国家分组<small>按订阅节点所在国家生成分组并放在节点前面<template v-if="countryPreview.length">，例如 {{ countryPreview.join('、') }}</template>；分组内自动选择延迟最低的节点。</small></span></label>
          <p v-if="draft.node_ids?.length" class="notice warning small">这个组在旧版本里指定了 {{ draft.node_ids.length }} 个单独节点，现在策略组不再支持指定节点，应用后会忽略它们，请改用国家或其他策略组。</p>
          <div class="form-field"><span class="field-label">国家 / 地区 <small class="muted">{{ draft.countries.length }} / 64</small></span><CountryFilter v-if="!readonly && !busy" v-model="draft.countries" :options="countryOptions" /><p v-else class="small">{{ draft.countries.map(countryName).join('、') || '未指定国家 / 地区' }}</p><span class="field-hint">按节点出口国家匹配，HK、TW、MO 各自独立；未选择时不按国家添加节点。</span><span v-if="optionsError" class="small danger-text">地区列表加载失败：{{ optionsError }} <button type="button" class="text-button" @click="loadCountries">重试</button></span></div>
          <div class="form-field"><span class="field-label">其他策略组 <small class="muted">{{ draft.groups.length }} / 64</small></span><p class="field-hint">按勾选顺序排在节点前面，支持嵌套，保存时检查循环引用。REJECT 放第一位即默认拦截。</p><div class="rule-group-ref-grid"><label v-for="name in refs" :key="name" class="rule-group-check"><input type="checkbox" :checked="draft.groups.includes(name)" :disabled="busy || readonly || (!draft.groups.includes(name) && draft.groups.length >= 64)" @change="toggleRef(name)" /><span>{{ groupLabel(name) }}</span></label></div></div>
        </fieldset>
        <p class="notice info small">成员只来自本次订阅已选中的节点；没有匹配成员时回退到 DIRECT，避免输出悬空引用。</p>
        <p v-if="error" class="notice error" role="alert">{{ error }}</p>
        <div :class="['rule-group-footer', { confirming: discardOpen }]">
          <div v-if="discardOpen" class="rule-group-discard" role="alert"><span><AppIcon name="alert" />放弃尚未应用的策略组修改？</span><div class="rule-group-discard-actions"><button ref="keepEditing" type="button" @click="discardOpen = false">继续编辑</button><button type="button" class="danger-solid" @click="close">放弃修改</button></div></div>
          <div v-else class="dialog-actions"><button type="button" :disabled="busy" @click="requestClose">{{ readonly ? '关闭' : '取消' }}</button><button v-if="!readonly" type="button" class="primary" :disabled="busy" @click="apply">应用策略组</button></div>
        </div>
      </div>
    </Modal>
  </section>
</template>
