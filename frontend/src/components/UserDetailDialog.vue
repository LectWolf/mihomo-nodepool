<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { errorText, formatDate, isCancelled, type Source } from '../api/system'
import { roleNames, type User } from '../api/accounts'
import { providerNames } from '../api/identity'
import { moderationApi, type UserDetail } from '../api/moderation'
import { levelLabel, userCommunity } from '../community'
import { toast } from '../toast'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import UserAvatar from './UserAvatar.vue'
import TitleChip from './TitleChip.vue'
import TakedownDialog from './TakedownDialog.vue'
// 用户详情：统计数据、第三方绑定与共享来源
const props = defineProps<{ user: User; manageable: boolean }>()
const emit = defineEmits<{ close: []; edit: [user: User]; changed: [] }>()
const detail = ref<UserDetail | null>(null)
const loading = ref(true)
const error = ref('')
const unlinking = ref('')
const confirmUnlink = ref('')
const target = ref<Source | null>(null)
const u = computed(() => detail.value?.user ?? props.user)
async function load() {
  loading.value = true
  try { detail.value = await moderationApi.userDetail(props.user.id); error.value = '' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
onMounted(load)
async function unlink(provider: string) {
  if (unlinking.value) return
  if (confirmUnlink.value !== provider) { confirmUnlink.value = provider; return }
  unlinking.value = provider
  try { await moderationApi.unlinkIdentity(props.user.id, provider); toast(`已解绑 ${providerNames[provider] ?? provider}`); confirmUnlink.value = ''; await load(); emit('changed') }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { unlinking.value = '' }
}
function moderated() { target.value = null; void load(); emit('changed') }
function identity(p: string) { return detail.value?.identities.find(i => i.provider === p) }
function when(v: string | null | undefined) { return v ? formatDate(v) : '—' }
interface StatItem { k: string; v: number | string; sub: string; tone?: string; text?: boolean }
const groups = computed<{ title: string; items: StatItem[] }[]>(() => {
  const s = detail.value?.stats
  if (!s) return []
  return [
    { title: '作为提供方', items: [
      { k: '共享来源', v: s.sources, sub: `上架 ${s.sources_active}${s.sources_taken_down ? ` · 已下架 ${s.sources_taken_down}` : ''}`, tone: s.sources_taken_down ? 'red' : '' },
      { k: '提供节点', v: s.provided_nodes, sub: '有效且未排除' },
      { k: '订阅用户', v: s.subscribers, sub: '占用其来源席位' },
      { k: '关注者', v: s.followers, sub: `关注了 ${s.following} 位提供方` },
    ] },
    { title: '作为订阅者', items: [
      { k: '组合订阅', v: s.subscriptions, sub: `占用他人席位 ${s.seats}` },
      { k: '7 天拉取', v: s.pulls_7d, sub: `${s.pull_ips_7d} 个 IP` },
      { k: '最近拉取', v: when(s.last_pull_at), sub: '客户端更新订阅', text: true },
      { k: '反馈', v: s.feedback, sub: '提交的节点反馈' },
    ] },
    { title: '账号活跃', items: [
      { k: '最近登录', v: when(s.last_login_at), sub: `${s.sessions} 个有效会话`, text: true },
      { k: '30 天积分', v: `+${s.points_earned_30d.toLocaleString()}`, sub: `消耗 ${s.points_spent_30d.toLocaleString()}`, text: true },
      { k: '30 天经验', v: `+${s.xp_earned_30d.toLocaleString()}`, sub: `当前 ${userCommunity(u.value).xp.toLocaleString()} XP`, text: true },
      { k: '注册时间', v: formatDate(u.value.created_at).split(' ')[0], sub: formatDate(u.value.created_at).split(' ')[1] ?? '', text: true },
    ] },
  ]
})
</script>

<template>
  <Modal title="用户详情" wide @close="emit('close')">
    <div class="user-detail">
      <header class="user-detail-head">
        <UserAvatar :id="u.id" :name="u.display_name || u.username" :version="u.avatar_version" :size="56" />
        <div class="user-detail-names">
          <strong>{{ u.display_name || u.username }}<TitleChip :name="u.title" /></strong>
          <span class="small muted">@{{ u.username }} · {{ roleNames[u.role] ?? u.role }} · {{ levelLabel(userCommunity(u).level) }} · {{ userCommunity(u).points.toLocaleString() }} 积分</span>
          <span class="user-detail-email"><AppIcon name="mail" /><template v-if="u.email">{{ u.email }}<span :class="['mini-tag', u.email_verified ? 'green' : 'amber']">{{ u.email_verified ? '已验证' : '未验证' }}</span></template><span v-else class="muted">未绑定邮箱</span></span>
        </div>
        <button v-if="manageable" type="button" @click="emit('edit', u)"><AppIcon name="edit" />编辑</button>
      </header>
      <p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
      <div v-if="loading && !detail" class="empty-state compact-empty" role="status"><span class="spinner"></span><p>正在读取…</p></div>
      <template v-if="detail">
        <section v-for="g in groups" :key="g.title" class="detail-group">
          <h3>{{ g.title }}</h3>
          <div class="detail-stats"><div v-for="item in g.items" :key="item.k" :data-tone="item.tone || undefined"><span>{{ item.k }}</span><strong :class="{ text: item.text }">{{ typeof item.v === 'number' ? item.v.toLocaleString() : item.v }}</strong><small>{{ item.sub }}</small></div></div>
        </section>
        <div class="detail-columns">
          <section class="detail-group">
            <h3>第三方账号</h3>
            <ul class="link-list">
              <li v-for="p in ['github', 'linuxdo']" :key="p">
                <span :class="['security-icon', p]"><AppIcon :name="p === 'github' ? 'github' : 'linuxdo'" /></span>
                <div><strong>{{ providerNames[p] }}</strong><small class="muted">{{ identity(p) ? `@${identity(p)!.login} · ${formatDate(identity(p)!.created_at).split(' ')[0]} 绑定` : '未绑定' }}</small></div>
                <button v-if="manageable && identity(p)" type="button" class="compact" :class="{ danger: confirmUnlink === p }" :disabled="!!unlinking" @click="unlink(p)"><span v-if="unlinking === p" class="spinner"></span>{{ confirmUnlink === p ? '确认解绑' : '解绑' }}</button>
              </li>
            </ul>
            <p class="field-hint">解绑后该用户无法再用此第三方账号登录，可重新绑定。</p>
          </section>
          <section class="detail-group">
            <h3>共享来源 <span class="count">{{ detail.sources.length }}</span></h3>
            <ul v-if="detail.sources.length" class="detail-sources">
              <li v-for="s in detail.sources" :key="s.id" :data-state="s.takedown ? 'takedown' : s.enabled ? 'active' : 'disabled'">
                <div><strong :title="s.name">{{ s.name }}</strong><span class="small muted">{{ s.node_count }} 个节点 · 席位 {{ s.occupied_users ?? s.active_users ?? 0 }}/{{ s.max_users || '∞' }}<template v-if="s.takedown"> · {{ s.takedown.reason_label }}</template></span></div>
                <span :class="['mini-tag', s.takedown ? 'red' : s.enabled ? 'green' : '']">{{ s.takedown ? '已下架' : s.enabled ? '上架中' : '已停用' }}</span>
                <button v-if="manageable" type="button" class="compact" :class="{ danger: !s.takedown }" @click="target = s">{{ s.takedown ? '恢复' : '下架' }}</button>
              </li>
            </ul>
            <p v-else class="small muted">还没有共享来源。</p>
          </section>
        </div>
      </template>
    </div>
    <TakedownDialog v-if="target" :source="target" @close="target = null" @done="moderated" />
  </Modal>
</template>
