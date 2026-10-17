<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import { absoluteURL, copyText, isGuest, requireLogin, type Subscription } from '../api/accounts'
import { communityUser, daysUntil, useAccountScope } from '../community'
import { usePolling } from '../polling'
import { navigate } from '../router'
import { toast } from '../toast'
import { cached, remember } from '../view-cache'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import SubscriptionQr from './SubscriptionQr.vue'
// 经典版首页顶部的“当前订阅”条：复制链接、二维码、剩余天数
const props = defineProps<{ paused: boolean }>()
const emit = defineEmits<{ checkin: [] }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const cacheKey = '/subscriptions?view=classic'
const items = ref<Subscription[]>(cached<Subscription[]>(cacheKey) ?? [])
const loaded = ref(items.value.length > 0)
const error = ref('')
const qrOpen = ref(false)
const pickKey = 'nodepool-classic-sub'
const formatKey = 'nodepool-sub-format'
const chosenID = ref(Number(readStorage(pickKey)) || 0)
function readStorage(key: string) { try { return localStorage.getItem(key) } catch { return null } }
const format = ref(readStorage(formatKey) || 'clash')
const current = computed(() => items.value.find(s => s.id === chosenID.value) ?? items.value[0])
const options = computed(() => items.value.map(s => ({ value: s.id, label: s.name })))
const selected = computed({ get: () => current.value?.id ?? 0, set: (id: number) => { chosenID.value = id; try { localStorage.setItem(pickKey, String(id)) } catch { /* 忽略 */ } } })
const days = computed(() => daysUntil(communityUser.value.subscription_expires_at))
const nodeCount = computed(() => current.value ? (current.value.node_count || 0) + (current.value.follow_node_count || 0) : 0)
const formatName = computed(() => format.value === 'v2ray' ? 'v2ray URI' : format.value === 'auto' ? '自动识别' : 'Clash / Mihomo')
function link(item: Subscription, f = format.value) { const value = new URL(absoluteURL(item.url)); if (f !== 'auto') value.searchParams.set('format', f); return value.href }
async function load() {
  if (props.paused || isGuest.value) return
  const ticket = scope.ticket()
  try {
    const result = await api<{ items: Subscription[] }>('/subscriptions')
    if (!scope.current(ticket)) return
    items.value = result.items ?? []; loaded.value = true; error.value = ''; remember(cacheKey, items.value)
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
}
watch(() => props.paused, paused => { if (!paused) void load() }, { immediate: true })
// 订阅只在用户自己操作时变化，低频刷新即可
usePolling(async () => { if (!props.paused) await load() }, 120000, false)
async function copy() {
  if (!current.value) return
  try { await copyText(link(current.value)); toast(`${formatName.value} 订阅链接已复制`) } catch (cause) { toast(errorText(cause), 'error') }
}
function qrFormat(value: string) { format.value = value; try { localStorage.setItem(formatKey, value) } catch { /* 忽略 */ } }
function importLink(item: Subscription) { return `clash://install-config?url=${encodeURIComponent(link(item, 'clash'))}&name=${encodeURIComponent('MCII节点共享')}` }
defineExpose({ load })
</script>

<template>
  <section class="classic-current" aria-label="当前订阅">
    <template v-if="isGuest">
      <span class="classic-current-label">当前订阅</span><span class="classic-current-name muted">登录后可挑选来源组合自己的订阅</span>
      <div class="classic-current-actions"><button type="button" class="primary compact" @click="requireLogin()">登录</button></div>
    </template>
    <template v-else-if="current">
      <span class="classic-current-label">当前订阅</span>
      <CustomSelect v-if="items.length > 1" v-model="selected" class="classic-current-select" label="选择当前订阅" :options="options" />
      <a v-else class="classic-current-name" href="/subscriptions" :title="current.name" @click.prevent="navigate('subscriptions')">{{ current.name }}</a>
      <div class="classic-current-meta"><span :class="{ 'danger-text': days === null || days <= 0, 'warn-text': days !== null && days > 0 && days <= 2 }">{{ days === null || days <= 0 ? '待签到激活' : `剩余 ${days} 天` }}</span><span>{{ nodeCount }} 节点</span><span v-if="current.follow_source_ids?.length">跟随 {{ current.follow_source_ids.length }} 个来源</span><span class="muted">{{ formatName }}</span></div>
      <div class="classic-current-actions">
        <button v-if="days === null || days <= 2" type="button" class="primary compact" @click="emit('checkin')"><AppIcon name="check" />签到</button>
        <button type="button" class="icon-button" title="复制订阅链接" aria-label="复制订阅链接" @click="copy"><AppIcon name="copy" /></button>
        <a v-if="format !== 'v2ray'" class="button icon-button" :href="importLink(current)" title="一键导入 Clash 系客户端" aria-label="一键导入 Clash 系客户端"><AppIcon name="download" /></a>
        <button type="button" class="icon-button" title="二维码" aria-label="显示订阅二维码" @click="qrOpen = true"><AppIcon name="qr" /></button>
        <button type="button" class="icon-button" title="管理订阅" aria-label="管理订阅" @click="navigate('subscriptions')"><AppIcon name="settings" /></button>
      </div>
    </template>
    <template v-else>
      <span class="classic-current-label">当前订阅</span><span class="classic-current-name muted">{{ error || (loaded ? '还没有订阅：在下方点开来源，跟随整个来源或挑选节点' : '正在读取订阅…') }}</span>
      <div class="classic-current-actions"><button v-if="error" type="button" class="compact" @click="load">重试</button><button type="button" class="compact" @click="emit('checkin')"><AppIcon name="check" />签到</button></div>
    </template>
    <SubscriptionQr v-if="qrOpen && current && !paused" :url="current.url" :title="`扫码导入 · ${current.name}`" :format="format" @update:format="qrFormat" @close="qrOpen = false" />
  </section>
</template>
