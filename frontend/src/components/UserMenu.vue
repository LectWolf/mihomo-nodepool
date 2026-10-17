<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { currentUser, roleNames } from '../api/accounts'
import { communityUser, levelLabel, levelProgress } from '../community'
import { navigate, type Section } from '../router'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import UserAvatar from './UserAvatar.vue'
defineProps<{ busy: boolean }>()
const emit = defineEmits<{ logout: []; tour: [] }>()
const open = ref(false)
const root = ref<HTMLElement | null>(null)
const name = computed(() => currentUser.value?.display_name || currentUser.value?.username || '')
const progress = computed(() => levelProgress(communityUser.value.xp))
const links = computed(() => [
  { id: 'account' as Section, label: '我的账号', icon: 'user', hint: '头像、签到、积分' },
  { id: 'subscriptions' as Section, label: '我的订阅', icon: 'link', hint: '订阅链接与节点' },
  { id: 'sources' as Section, label: '共享来源', icon: 'sources', hint: '我提供的来源' },
  ...(currentUser.value?.role === 'admin' ? [{ id: 'settings' as Section, label: '运行设置', icon: 'settings', hint: '系统与社区规则' }] : []),
])
function go(id: Section) { open.value = false; navigate(id) }
function outside(event: PointerEvent) { if (open.value && root.value && !root.value.contains(event.target as Node)) open.value = false }
function key(event: KeyboardEvent) { if (event.key === 'Escape' && open.value) { open.value = false; root.value?.querySelector<HTMLButtonElement>('.user-menu-trigger')?.focus() } }
onMounted(() => { document.addEventListener('pointerdown', outside); document.addEventListener('keydown', key) })
onBeforeUnmount(() => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', key) })
</script>

<template>
  <div ref="root" class="user-menu">
    <button type="button" class="user-menu-trigger" :aria-expanded="open" aria-haspopup="menu" aria-label="打开账号菜单" @click="open = !open">
      <UserAvatar :id="currentUser?.id" :name="name" :version="currentUser?.avatar_version" :size="32" />
      <span class="user-menu-name"><strong>{{ name }}</strong><small>{{ levelLabel(communityUser.level) }}</small></span>
      <AppIcon name="down" />
    </button>
    <Transition name="pop">
      <div v-if="open" class="user-menu-panel" role="menu">
        <div class="user-menu-card">
          <UserAvatar :id="currentUser?.id" :name="name" :version="currentUser?.avatar_version" :size="52" />
          <div><strong>{{ name }}</strong><span class="small muted">@{{ currentUser?.username }} · {{ roleNames[currentUser?.role ?? 'user'] }}</span><TitleChip :name="currentUser?.title" /></div>
        </div>
        <div class="user-menu-level">
          <div><span class="level-badge">Lv{{ communityUser.level }}</span><strong>{{ levelLabel(communityUser.level).split(' ')[1] }}</strong><span class="small muted">{{ progress.next == null ? '已满级' : `${communityUser.xp.toLocaleString()} / ${progress.next.toLocaleString()} XP` }}</span></div>
          <div class="xp-track"><i :style="{ width: `${progress.percent}%` }"></i></div>
          <div class="user-menu-stats"><span><AppIcon name="coin" />{{ communityUser.points.toLocaleString() }} 积分</span><span><AppIcon name="star" />连签 {{ communityUser.checkin_streak }} 天</span></div>
        </div>
        <button v-for="link in links" :key="link.id" type="button" role="menuitem" class="user-menu-item" @click="go(link.id)"><AppIcon :name="link.icon" /><span><strong>{{ link.label }}</strong><small>{{ link.hint }}</small></span></button>
        <button type="button" role="menuitem" class="user-menu-item" @click="open = false; emit('tour')"><AppIcon name="compass" /><span><strong>新手教程</strong><small>重新查看各页面介绍</small></span></button>
        <button type="button" role="menuitem" class="user-menu-item danger" :disabled="busy" @click="open = false; emit('logout')"><AppIcon name="logout" /><span><strong>{{ busy ? '正在退出…' : '退出登录' }}</strong></span></button>
      </div>
    </Transition>
  </div>
</template>
