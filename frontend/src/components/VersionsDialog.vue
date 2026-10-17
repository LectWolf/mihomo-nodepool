<script setup lang="ts">
import { computed, ref } from 'vue'
import { errorText, formatDate, isCancelled, json, safeReleaseUrl, useApi, type Releases, type SystemInfo } from '../api/system'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ system?: SystemInfo | null }>()
const emit = defineEmits<{ close: []; restart: [] }>()
const shortCommit = computed(() => props.system?.commit && props.system.commit !== 'unknown' ? props.system.commit.slice(0, 7) : '')
const built = computed(() => props.system?.build_time && !props.system.build_time.startsWith('unknown') ? formatDate(props.system.build_time) : '')
const day = (value: string) => value ? formatDate(value).split(' ')[0] : ''
const api = useApi()
const data = ref<Releases | null>(null)
const loading = ref(false)
const loadError = ref('')
const actionError = ref('')
const notice = ref('')
const selected = ref('')
const submitting = ref(false)
const waitingForTask = ref(false)
const confirm = ref<'update' | 'history' | 'backup' | 'restart' | null>(null)
let submittedAt = 0
let initialState = ''
const activeStates = ['checking', 'downloading', 'installing', 'restarting']
const running = computed(() => !!data.value && activeStates.includes(data.value.state))
const busy = computed(() => submitting.value || waitingForTask.value || running.value)
const selectedRelease = computed(() => data.value?.releases.find(release => release.version === selected.value))
const stateLabels = { idle: '待命', checking: '正在检查', downloading: '正在下载', installing: '正在安装', restarting: '正在重启', done: '操作完成', error: '操作失败' }
const canInstall = computed(() => !!selectedRelease.value && !!data.value?.can_update && selected.value !== data.value.current_version && !data.value.need_restart && !busy.value)
async function load() {
  if (loading.value) return
  loading.value = true
  try {
    const value = await api<Releases>('/releases')
    data.value = value; loadError.value = ''
    if (!selected.value) selected.value = value.releases.find(release => release.version === value.latest_version)?.version ?? value.releases[0]?.version ?? ''
    if (waitingForTask.value) {
      if (activeStates.includes(value.state) || ((value.state === 'done' || value.state === 'error') && `${value.state}:${value.message}:${value.need_restart}` !== initialState)) waitingForTask.value = false
      else if (Date.now() - submittedAt > 120000) {
        waitingForTask.value = false
        actionError.value = '暂未确认任务状态，请重新检查版本状态后再操作。'
      }
    }
  } catch (error) { if (!isCancelled(error)) loadError.value = errorText(error) }
  finally { loading.value = false }
}
usePolling(load, 2500)
async function execute() {
  const action = confirm.value
  if (!action || submitting.value || busy.value) return
  submitting.value = true; actionError.value = ''; notice.value = ''
  try {
    if (action === 'restart') {
      await api('/system/restart', json('POST'))
      confirm.value = null
      emit('restart')
    } else {
      const path = action === 'update' ? '/system/update' : '/system/rollback'
      const payload = action === 'backup' ? {} : { version: selected.value }
      initialState = `${data.value?.state}:${data.value?.message}:${data.value?.need_restart}`
      await api(path, json('POST', payload))
      submittedAt = Date.now()
      waitingForTask.value = true
      notice.value = '任务已提交，正在跟踪执行进度。'
      confirm.value = null
      await load()
    }
  } catch (error) { if (!isCancelled(error)) { actionError.value = errorText(error); confirm.value = null } }
  finally { submitting.value = false }
}
const confirmationText = computed(() => {
  if (confirm.value === 'restart') return '立即重启服务？连接会短暂中断，控制台会自动尝试重新连接。'
  if (confirm.value === 'backup') return '恢复上一次保存在本地的程序备份？完成后需要重启服务。回退仅切换程序文件，数据库保持当前内容。'
  return `确定${confirm.value === 'history' ? '回退' : '安装'}到 ${selected.value}？完成后需要重启服务，数据库保持当前内容。`
})
</script>

<template>
  <Modal title="版本与发布" wide :busy="submitting" @close="emit('close')">
    <div v-if="!data && loading" class="empty-state" role="status"><span class="spinner"></span><h3>正在获取发布版本</h3></div>
    <div v-if="loadError" class="notice error" role="alert">{{ loadError }}<button :disabled="loading" @click="load">重新检查</button></div>
    <template v-if="data">
      <section :class="['version-hero', { update: data.update_available }]">
        <span class="brand-symbol">M</span>
        <div class="version-hero-main">
          <span class="eyebrow">MCII 节点共享</span>
          <strong>{{ data.current_version || '未提供版本' }}</strong>
          <span class="version-hero-meta"><span v-if="shortCommit"><AppIcon name="link" />{{ shortCommit }}</span><span v-if="built"><AppIcon name="clock" />构建于 {{ built }}</span><span v-if="system?.core?.version"><AppIcon name="probe" />内核 {{ system.core.version }}</span></span>
        </div>
        <div class="version-hero-status">
          <span :class="['version-state', data.update_available ? 'update' : 'latest']"><AppIcon :name="data.update_available ? 'upload' : 'check'" />{{ data.update_available ? '有可用更新' : '已是最新' }}</span>
          <small v-if="data.update_available" class="muted">最新稳定版 {{ data.latest_version }}</small>
          <button type="button" class="compact" :disabled="loading || busy" @click="load"><AppIcon name="refresh" />{{ loading ? '检查中…' : '检查更新' }}</button>
        </div>
      </section>
      <div v-if="data.need_restart" class="notice warning" role="status">程序文件已准备好，请重启服务以使用所选版本。</div>
      <div v-if="running || waitingForTask || data.state === 'error' || data.message" :class="['notice', data.state === 'error' ? 'error' : 'info']" role="status"><span v-if="running || waitingForTask" class="spinner"></span><strong>{{ waitingForTask ? '等待任务开始' : stateLabels[data.state] }}</strong><span>{{ data.message }}</span></div>
      <p v-if="notice && !running && !data.need_restart && data.state !== 'error'" class="small green-text" role="status">{{ notice }}</p>
      <div v-if="data.releases.length" class="release-layout">
        <ol class="release-timeline" aria-label="发布版本">
          <li v-for="release in data.releases" :key="release.version"><button type="button" :class="{ active: selected === release.version }" :aria-pressed="selected === release.version" :disabled="busy" @click="selected = release.version"><span class="release-dot"></span><span class="release-line"><strong>{{ release.version }}</strong><small>{{ day(release.published_at) }}</small></span><span class="release-tags"><em v-if="release.version === data.latest_version" class="tag-latest">最新</em><em v-if="release.version === data.current_version" class="tag-current">当前</em></span></button></li>
        </ol>
        <article v-if="selectedRelease" class="release-notes">
          <div class="release-notes-head"><div><h3>{{ selectedRelease.name || selectedRelease.version }}</h3><p class="small muted">发布于 {{ formatDate(selectedRelease.published_at) }}</p></div><a v-if="safeReleaseUrl(selectedRelease.url)" :href="safeReleaseUrl(selectedRelease.url)" target="_blank" rel="noopener noreferrer" class="small green-text">查看原文 <AppIcon name="external" /></a></div>
          <div class="release-body">{{ selectedRelease.body || '此版本未提供发布说明。' }}</div>
          <div class="release-actions"><button class="primary" :disabled="!canInstall" @click="confirm = 'update'"><AppIcon name="upload" />{{ selected === data.current_version ? '当前正在使用' : '安装此版本' }}</button><button :disabled="!canInstall || selected === data.latest_version" @click="confirm = 'history'"><AppIcon name="left" />回退到此版本</button></div>
        </article>
      </div>
      <div v-else class="empty-state"><span class="empty-icon"><AppIcon name="version" /></span><h3>暂无发布记录</h3><p>获取到的发布版本将在这里显示。</p></div>
      <div v-if="!data.can_update" class="notice warning">{{ data.message || '当前运行环境不支持在线更新。' }}</div>
      <div class="version-tools">
        <div class="version-tool"><span class="version-tool-icon"><AppIcon name="reset" /></span><div><strong>恢复本地备份</strong><p class="small muted">恢复上一次备份的程序文件，随后重启生效。</p></div><button :disabled="!data.can_rollback || busy || data.need_restart" @click="confirm = 'backup'">恢复备份</button></div>
        <div class="version-tool"><span class="version-tool-icon"><AppIcon name="refresh" /></span><div><strong>重启服务</strong><p class="small muted">连接会短暂中断，恢复后自动重连。</p></div><button :class="{ primary: data.need_restart }" :disabled="busy" @click="confirm = 'restart'">立即重启</button></div>
      </div>
      <p class="small muted version-foot">容器重新创建后会使用所选镜像中的版本；版本回退只替换程序，不回退数据库。</p>
    </template>
    <p v-if="actionError" class="notice error" role="alert">{{ actionError }}</p>
    <div v-if="confirm" class="confirmation-panel" role="alert"><p>{{ confirmationText }}</p><div class="dialog-actions"><button :disabled="submitting" @click="confirm = null">取消</button><button class="primary" :disabled="busy" @click="execute">{{ submitting ? '提交中…' : '确认操作' }}</button></div></div>
  </Modal>
</template>
