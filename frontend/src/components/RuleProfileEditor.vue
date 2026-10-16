<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { RuleProfile, RuleGroup } from '../api/accounts'
import CustomSelect from './CustomSelect.vue'
import AppIcon from './AppIcon.vue'
import RuleGroupEditor from './RuleGroupEditor.vue'
import '../rule-groups.css'
function copyGroups(groups: RuleGroup[] | null | undefined): RuleGroup[] {
  if (groups == null) {
    const create = (name: string, type: RuleGroup['type'], include_all = false, refs: string[] = []): RuleGroup => ({ name, type, include_all, node_ids: [], countries: [], groups: refs, interval: 300, tolerance: 50 })
    return [create('NodePool', 'select', true, ['♻️ 自动选择', '🚑 故障转移']), create('♻️ 自动选择', 'url-test', true), create('🚑 故障转移', 'fallback', true), create('🌩️ Cloudflare', 'select', false, ['NodePool', 'DIRECT']), create('🇨🇳 国内网站', 'select', false, ['DIRECT', 'NodePool']), create('🛑 拦截广告', 'select', false, ['REJECT', 'DIRECT']), create('🐟 漏网之鱼', 'select', false, ['NodePool', 'DIRECT'])]
  }
  return groups.map(group => ({ ...group, node_ids: [...(group.node_ids ?? [])], countries: [...(group.countries ?? [])], groups: [...(group.groups ?? [])], interval: group.interval || 300, tolerance: group.tolerance ?? 50 }))
}
const props = defineProps<{ profile: RuleProfile; busy: boolean; error: string; readonly?: boolean }>()
const emit = defineEmits<{ save: [value: RuleProfile]; cancel: []; copy: [] }>()
const form = reactive({ name: props.profile.name, routing: props.profile.routing_enabled, dnsEnabled: props.profile.dns_enabled, dns: props.profile.dns_servers.join('\n'), text: props.profile.rules.join('\n'), groups: copyGroups(props.profile.groups) })
const tab = ref<'routing' | 'groups' | 'dns'>('routing')
const mode = ref<'list' | 'text'>('list')
const search = ref('')
const page = ref(1)
const builder = reactive({ type: 'DOMAIN-SUFFIX', payload: '', target: 'NodePool', index: -1 })
const builderError = ref('')
const localError = ref('')
const restoreOpen = ref(false)
const rows = computed(() => form.text.split('\n').map((raw, index) => {
  const parts = raw.trim().split(','); const match = parts[0] === 'MATCH'
  return { index, raw, type: parts[0] || '', payload: match ? '所有剩余流量' : parts[1] || '', target: parts[match ? 1 : 2] || '' }
}).filter(row => row.raw.trim() && !row.raw.trim().startsWith('#')))
const matches = computed(() => rows.value.filter(row => row.raw.toLowerCase().includes(search.value.trim().toLowerCase())))
const pages = computed(() => Math.max(1, Math.ceil(matches.value.length / 30)))
const visibleRows = computed(() => matches.value.slice((page.value - 1) * 30, page.value * 30))
const dnsServers = computed(() => form.dns.split(/[\s,，;；]+/).filter(Boolean))
const targets = computed(() => [{ value: 'NodePool', label: '节点选择 · NodePool' }, { value: 'DIRECT', label: '直连 · DIRECT' }, { value: 'REJECT', label: '拦截 · REJECT' }, ...form.groups.filter(group => group.name !== 'NodePool').map(group => ({ value: group.name, label: group.name }))])
const types = [{ value: 'DOMAIN-SUFFIX', label: '域名后缀' }, { value: 'DOMAIN', label: '完整域名' }, { value: 'DOMAIN-KEYWORD', label: '域名关键词' }, { value: 'IP-CIDR', label: 'IPv4 网段' }, { value: 'IP-CIDR6', label: 'IPv6 网段' }, { value: 'DST-PORT', label: '目标端口' }, { value: 'SRC-PORT', label: '来源端口' }, { value: 'NETWORK', label: '网络类型' }, { value: 'GEOIP', label: 'IP 地区' }, { value: 'MATCH', label: '兜底规则' }]
const targetLabel = (target: string) => targets.value.find(option => option.value === target)?.label || target
const payloadHint = computed(() => builder.type === 'GEOIP' ? 'CN 或 LAN' : builder.type === 'NETWORK' ? 'tcp 或 udp' : builder.type.includes('PORT') ? '443 或 8000-9000' : builder.type.includes('CIDR') ? (builder.type === 'IP-CIDR6' ? '2001:db8::/32' : '192.0.2.0/24') : 'example.com')
const changed = computed(() => form.name !== props.profile.name || form.routing !== props.profile.routing_enabled || form.dnsEnabled !== props.profile.dns_enabled || form.dns !== props.profile.dns_servers.join('\n') || form.text !== props.profile.rules.join('\n') || JSON.stringify(form.groups) !== JSON.stringify(copyGroups(props.profile.groups)))
const builderPending = computed(() => builder.index >= 0 || !!builder.payload.trim() || builder.type !== 'DOMAIN-SUFFIX' || builder.target !== 'NodePool')
watch(search, () => { page.value = 1 })
watch(pages, max => { if (page.value > max) page.value = max })
function resetBuilder() { Object.assign(builder, { type: 'DOMAIN-SUFFIX', payload: '', target: 'NodePool', index: -1 }); builderError.value = '' }
function editRow(row: typeof rows.value[number]) { Object.assign(builder, { type: row.type, payload: row.type === 'MATCH' ? '' : row.payload, target: row.target, index: row.index }); builderError.value = '' }
function applyRule() {
  builderError.value = ''
  if (builder.type !== 'MATCH' && (!builder.payload.trim() || /[,\r\n]/.test(builder.payload))) { builderError.value = '请填写单个有效匹配内容，不要包含逗号或换行。'; return }
  if (!targets.value.some(option => option.value === builder.target)) { builderError.value = '请选择存在的策略组。'; return }
  const oldLines = form.text.split('\n')
  const old = builder.index >= 0 ? oldLines[builder.index]?.split(',') : undefined
  const line = [builder.type, ...(builder.type === 'MATCH' ? [] : [builder.payload.trim()]), builder.target, ...(old?.at(-1)?.trim() === 'no-resolve' && ['IP-CIDR','IP-CIDR6','GEOIP'].includes(builder.type) ? ['no-resolve'] : [])].join(',')
  if (builder.index >= 0) oldLines[builder.index] = line
  else if (builder.type === 'MATCH') {
    const index = oldLines.findIndex(value => value.trim().startsWith('MATCH,'))
    if (index >= 0) oldLines[index] = line; else oldLines.push(line)
  } else oldLines.unshift(line)
  form.text = oldLines.join('\n'); resetBuilder(); search.value = ''; page.value = 1
}
function removeRow(index: number) { const lines = form.text.split('\n'); lines.splice(index, 1); form.text = lines.join('\n'); resetBuilder() }
function moveRow(index: number, direction: number) { const lines = form.text.split('\n'); const next = index + direction; if (next < 0 || next >= lines.length) return; [lines[index], lines[next]] = [lines[next]!, lines[index]!]; form.text = lines.join('\n'); resetBuilder() }
function renameGroup(oldName: string, name: string) {
  form.text = form.text.split('\n').map(line => {
    const parts = line.split(','); const target = parts[0]?.trim() === 'MATCH' ? 1 : 2
    if (parts[target]?.trim() === oldName) { parts[target] = name; return parts.join(',') }
    return line
  }).join('\n')
  if (builder.target === oldName) builder.target = name
}
function restore() { form.name = props.profile.name; form.text = props.profile.rules.join('\n'); form.routing = props.profile.routing_enabled; form.dns = props.profile.dns_servers.join('\n'); form.dnsEnabled = props.profile.dns_enabled; form.groups = copyGroups(props.profile.groups); restoreOpen.value = false; localError.value = ''; resetBuilder() }
function save() {
  localError.value = ''
  if (builderPending.value) { localError.value = '有尚未应用的规则，请先添加、应用或取消当前规则编辑。'; tab.value = 'routing'; mode.value = 'list'; return }
  emit('save', { ...props.profile, name: form.name.trim(), routing_enabled: form.routing, dns_enabled: form.dnsEnabled, dns_servers: dnsServers.value, rules: rows.value.map(row => row.raw.trim()), groups: copyGroups(form.groups) })
}
</script>
<template>
  <form class="rule-workbench" @submit.prevent="save">
    <div class="rule-workbench-header"><label v-if="!readonly" class="form-field"><span class="field-label">组合名称</span><input v-model="form.name" required maxlength="200" :disabled="busy" /></label><div v-else><span class="official-label"><AppIcon name="check" />官方提供</span><h3>{{ form.name }}</h3><p class="small muted">所有账号可直接使用。复制后即可按自己的需要修改。</p></div><span v-if="!readonly" class="draft-state" :class="{ dirty: changed }">{{ changed ? '有未保存修改' : '已与保存内容一致' }}</span></div>
    <div class="rule-workbench-body">
      <aside class="rule-workbench-nav"><button type="button" :aria-pressed="tab === 'routing'" @click="tab = 'routing'"><AppIcon name="sources" /><span>分流规则<small>{{ rows.length.toLocaleString() }} 条 · {{ form.routing ? '已开启' : '跟随来源' }}</small></span></button><button type="button" :aria-pressed="tab === 'groups'" @click="tab = 'groups'"><AppIcon name="nodes" /><span>策略组<small>{{ form.groups.length }} 组 · 节点与国家</small></span></button><button type="button" :aria-pressed="tab === 'dns'" @click="tab = 'dns'"><AppIcon name="globe" /><span>DNS 设置<small>{{ form.dnsEnabled ? dnsServers.length ? `${dnsServers.length} 个服务器` : '默认 DNS' : '已关闭' }}</small></span></button><div class="rule-order-note"><AppIcon name="info" /><p>规则从上到下匹配。新规则添加在最前面，MATCH 用作最后兜底。</p></div></aside>
      <div class="rule-workbench-content">
        <section v-show="tab === 'routing'" class="routing-section">
          <div class="workbench-section-heading"><div><h3>分流规则</h3><p>国内直连、广告拦截与代理策略。关闭后跟随来源。</p></div><button type="button" class="switch" :class="{ on: form.routing }" role="switch" aria-label="分流规则" :aria-checked="form.routing" :disabled="busy || readonly" @click="form.routing = !form.routing"><span></span></button></div>
          <p v-if="!form.routing" class="notice info small">当前不应用下面的覆盖规则，内容仍会保留。</p>
          <div class="rule-list-toolbar"><label class="search-field"><span class="sr-only">搜索规则</span><AppIcon name="search" /><input v-model="search" type="search" placeholder="搜索域名、类型或处理方式" /></label><div v-if="!readonly" class="scope-tabs" role="group" aria-label="规则编辑方式"><button type="button" :aria-pressed="mode === 'list'" :disabled="busy" @click="mode = 'list'">规则列表</button><button type="button" :aria-pressed="mode === 'text'" :disabled="busy || builderPending" @click="mode = 'text'">文本编辑</button></div></div>
          <p v-if="builderPending && !readonly" class="small muted" role="status">请先应用或取消当前规则编辑，再切换文本模式。</p>
          <template v-if="mode === 'list' || readonly">
            <fieldset v-if="!readonly" class="rule-builder" :disabled="busy"><legend>{{ builder.index >= 0 ? `编辑第 ${builder.index + 1} 条规则` : '添加优先规则' }}</legend><div class="rule-builder-fields"><div class="form-field"><span class="field-label">匹配类型</span><CustomSelect v-model="builder.type" label="规则匹配类型" :options="types" :disabled="busy" /></div><label class="form-field"><span class="field-label">匹配内容</span><input v-model="builder.payload" :disabled="builder.type === 'MATCH'" :placeholder="builder.type === 'MATCH' ? '所有剩余流量' : payloadHint" /></label><div class="form-field"><span class="field-label">处理方式</span><CustomSelect v-model="builder.target" label="规则处理方式" :options="targets" :disabled="busy" /></div></div><div class="rule-builder-actions"><span v-if="builderError" class="danger-text small" role="alert">{{ builderError }}</span><button v-if="builderPending" type="button" class="text-button" @click="resetBuilder">取消编辑</button><button type="button" class="compact" @click="applyRule"><AppIcon name="plus" />{{ builder.index >= 0 ? '应用修改' : '添加规则' }}</button></div></fieldset>
            <div class="rule-table-wrap"><table class="rule-table"><thead><tr><th>顺序</th><th>匹配规则</th><th>处理方式</th><th v-if="!readonly"><span class="sr-only">操作</span></th></tr></thead><tbody><tr v-for="row in visibleRows" :key="row.index" :class="{ 'editing-rule': builder.index === row.index }"><td class="mono">{{ row.index + 1 }}</td><td><strong :title="row.payload">{{ row.payload }}</strong><small>{{ row.type }}</small></td><td><span class="rule-target" :class="{ direct: row.target === 'DIRECT', reject: row.target === 'REJECT' }">{{ targetLabel(row.target) }}</span></td><td v-if="!readonly"><div class="rule-row-actions"><button type="button" class="icon-button" :aria-label="`编辑第 ${row.index + 1} 条规则`" title="编辑" :disabled="busy" @click="editRow(row)"><AppIcon name="edit" /></button><button type="button" class="icon-button" :aria-label="`上移第 ${row.index + 1} 条规则`" title="上移" :disabled="busy || row.index === 0" @click="moveRow(row.index, -1)"><AppIcon name="up" /></button><button type="button" class="icon-button" :aria-label="`下移第 ${row.index + 1} 条规则`" title="下移" :disabled="busy || row.index >= form.text.split('\n').length - 1" @click="moveRow(row.index, 1)"><AppIcon name="down" /></button><button type="button" class="icon-button" :aria-label="`移除第 ${row.index + 1} 条规则`" title="移除" :disabled="busy" @click="removeRow(row.index)"><AppIcon name="close" /></button></div></td></tr></tbody></table><p v-if="!visibleRows.length" class="rule-no-results small muted">没有匹配的规则</p></div>
            <div class="compact-pagination"><span class="small muted">{{ matches.length.toLocaleString() }} 条匹配 · 每页 30 条</span><button type="button" class="icon-button" aria-label="上一页规则" :disabled="page <= 1" @click="page--"><AppIcon name="left" /></button><span>{{ page }} / {{ pages }}</span><button type="button" class="icon-button" aria-label="下一页规则" :disabled="page >= pages" @click="page++"><AppIcon name="right" /></button></div>
          </template>
          <div v-else class="rule-text-mode"><label class="form-field"><span class="field-label">Clash 规则 <small class="muted">每行一条，按顺序匹配</small></span><textarea v-model="form.text" class="mono rule-textarea" spellcheck="false" :disabled="busy" aria-label="Clash 分流规则" /><span class="field-hint">支持域名、IP 网段、端口、NETWORK、GEOIP(CN/LAN) 和 MATCH。保存时校验格式与目标。</span></label><p class="small muted">{{ rows.length.toLocaleString() }} 条规则 · 空行与 # 注释不会写入订阅。</p></div>
        </section>
        <div v-show="tab === 'groups'"><p v-if="!form.routing" class="notice info small">分流规则关闭时，策略组仍会保存，但订阅继续使用缓存来源的原有路由。</p><RuleGroupEditor v-model="form.groups" :busy="busy" :readonly="readonly" :rule-targets="[...rows.map(row => row.target), ...(builderPending ? [builder.target] : [])]" @rename="renameGroup" /></div>
        <section v-show="tab === 'dns'" class="dns-section"><div class="workbench-section-heading"><div><h3>自定义 DNS</h3><p>仅覆盖 Clash 的 DNS，关闭后不写入 DNS 配置。</p></div><button type="button" class="switch" :class="{ on: form.dnsEnabled }" role="switch" aria-label="自定义 DNS" :aria-checked="form.dnsEnabled" :disabled="busy || readonly" @click="form.dnsEnabled = !form.dnsEnabled"><span></span></button></div><div class="dns-default-card"><AppIcon name="globe" /><div><strong>{{ form.dnsEnabled ? dnsServers.length ? '自定义服务器' : '使用默认 DNS' : 'DNS 覆盖已关闭' }}</strong><p>{{ form.dnsEnabled ? dnsServers.length ? dnsServers.join(' · ') : '223.5.5.5 · 119.29.29.29 · 8.8.8.8' : '已填写内容保留，重新开启即可恢复。' }}</p></div></div><label class="form-field"><span class="field-label">DNS 服务器 <small class="muted">可选 · 留空使用默认值</small></span><textarea v-model="form.dns" class="mono dns-input" rows="6" placeholder="223.5.5.5&#10;119.29.29.29&#10;8.8.8.8" :disabled="busy || readonly || !form.dnsEnabled" /><span class="field-hint">每行一个，或用逗号分隔。支持 IP、HTTPS、TLS、UDP 和 TCP 地址，最多 32 项。</span></label><button v-if="!readonly && form.dns" type="button" class="text-button" :disabled="busy || !form.dnsEnabled" @click="form.dns = ''">使用默认 DNS</button></section>
      </div>
    </div>
    <p v-if="error || localError" role="alert" class="notice error">{{ error || localError }}</p>
    <div v-if="restoreOpen" class="notice warning rule-restore"><span>恢复打开编辑器时的内容，放弃当前修改？</span><button type="button" @click="restoreOpen = false">保留修改</button><button type="button" @click="restore">确认恢复</button></div>
    <div class="dialog-actions workbench-footer"><button v-if="!readonly && changed" type="button" class="text-button" :disabled="busy" @click="restoreOpen = true"><AppIcon name="reset" />撤销本次修改</button><span class="small muted">仅影响 Clash / Mihomo</span><button type="button" :disabled="busy" @click="emit('cancel')">{{ readonly ? '关闭' : '取消' }}</button><button v-if="readonly" type="button" class="primary" @click="emit('copy')"><AppIcon name="copy" />复制为私人组合</button><button v-else class="primary" :disabled="busy || !form.name.trim()">{{ busy ? '正在保存…' : '保存规则组合' }}</button></div>
  </form>
</template>
