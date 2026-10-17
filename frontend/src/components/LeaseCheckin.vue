<script setup lang="ts">
import { toastRef } from '../toast'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { currentUser, loadProfile } from '../api/accounts'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { beijingDate, communityUser, levelLabel, levelNames, levelThresholds, useAccountScope, type CheckinResult } from '../community'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'

const props = defineProps<{ paused?: boolean }>()
const emit = defineEmits<{ renewed: [] }>()
const api = useApi()
const scope = useAccountScope(() => !!props.paused)
const busy = ref(false)
const error = ref('')
const message = toastRef()
const pending = ref<CheckinResult | null>(null)
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 60000)
onBeforeUnmount(() => clearInterval(timer))
watch(() => props.paused, paused => { if (paused) { pending.value = null; error.value = ''; message.value = '' } })
const user = communityUser
const checked = computed(() => user.value.checkin_on === beijingDate(new Date(now.value)))
const nextXP = computed(() => levelThresholds[user.value.level + 1])
const progress = computed(() => {
  const start = levelThresholds[user.value.level] ?? 0
  return nextXP.value ? Math.max(0, Math.min(100, (user.value.xp - start) / (nextXP.value - start) * 100)) : 100
})
const active = computed(() => !!user.value.subscription_expires_at && new Date(user.value.subscription_expires_at).getTime() > now.value)
async function checkin(confirmPartial = false) {
  if (busy.value || props.paused || !currentUser.value || (confirmPartial && !pending.value)) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''; message.value = ''
  try {
    const result = await api<CheckinResult>('/me/checkin', json('POST', { confirm_partial: confirmPartial }))
    if (!scope.current(ticket)) return
    if (result.needs_confirmation) { pending.value = result; return }
    pending.value = null
    if (result.user.id === currentUser.value?.id) currentUser.value = result.user
    message.value = result.awarded ? `签到成功，经验 +${result.xp_added}，积分 +${result.points_added}，订阅已激活。` : '订阅已重新激活并续期，今日奖励已领取，不重复发放。'
    now.value = Date.now()
    emit('renewed')
    try { await loadProfile() } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = `激活成功，用量更新失败：${errorText(cause)}` }
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <section class="panel lease-card" aria-label="等级与订阅激活">
    <div class="lease-top">
      <div class="level-identity"><span class="level-emblem">{{ user.level }}</span><div><strong>{{ levelLabel(user.level) }}</strong><p class="small muted">连续签到 {{ user.checkin_streak }} 天 · {{ user.points.toLocaleString() }} 积分</p></div></div>
      <button class="primary" :disabled="busy || paused" @click="checkin(false)"><AppIcon name="check" />{{ busy ? '处理中…' : checked ? '已签到 · 激活续期' : '签到并激活订阅' }}</button>
    </div>
    <div class="level-progress-copy"><span>{{ user.xp.toLocaleString() }} XP</span><span>{{ nextXP ? `距 Lv${user.level + 1} 还需 ${Math.max(0, nextXP - user.xp).toLocaleString()} XP` : '已达到最高等级' }}</span></div>
    <div class="level-progress" role="progressbar" aria-label="当前等级进度" :aria-valuenow="Math.round(progress)" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: `${progress}%` }"></span></div>
    <div class="lease-status"><span :class="active ? 'green-text' : 'danger-text'">{{ active ? `订阅有效至 ${formatDate(user.subscription_expires_at)}` : '订阅待激活或已到期，请在网页签到激活' }}</span><small class="muted">北京时间每日 +5 XP / +2 积分，每连续 7 天额外 +20 XP / +8 积分</small></div>
    <p class="small muted">默认租期 14 天（以站点设置为准）。到期拉取会提示回网页激活；有效探测可续期，浏览页面和普通下载不会续期。今日已签到仍可点击激活。</p>
    <details class="level-thresholds"><summary class="small">等级门槛</summary><div class="level-steps"><div v-for="(name, index) in levelNames" :key="index" :class="{ reached: user.level >= index }"><strong>Lv{{ index }} {{ name }}</strong><small>{{ levelThresholds[index].toLocaleString() }} XP</small></div></div></details>

    <p v-if="error && !pending" class="notice error small" role="alert">{{ error }}</p>
    <Modal v-if="pending && !paused" title="确认部分激活" :busy="busy" @close="pending = null">
      <p>以下来源当前无法激活。本次尚未提交签到，是否从你的所有组合中移除这些来源，再激活其余来源？</p>
      <ul class="checkin-skipped"><li v-for="source in pending.skipped" :key="source.source_id"><strong :title="source.name">{{ source.name }}</strong><span>{{ source.reason }}</span></li></ul>
      <p class="small muted">取消会保留原组合。确认移除后，可在满足条件时重新选择来源。</p>
      <p v-if="error" class="notice error" role="alert">{{ error }}</p>
      <div class="dialog-actions"><button :disabled="busy" @click="pending = null">取消，保留组合</button><button class="danger-solid" :disabled="busy" @click="checkin(true)">移除以上来源并激活</button></div>
    </Modal>
  </section>
</template>
