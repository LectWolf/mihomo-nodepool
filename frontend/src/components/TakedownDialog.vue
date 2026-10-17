<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { errorText, formatDate, isCancelled, type Source } from '../api/system'
import { moderationApi, takedownReasons, type ModerationEvent } from '../api/moderation'
import { toast } from '../toast'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import UserAvatar from './UserAvatar.vue'
// 下架或恢复来源：下架需选择原因，可选通知提供方
const props = defineProps<{ source: Source }>()
const emit = defineEmits<{ close: []; done: [source: Source] }>()
const restoring = computed(() => !!props.source.takedown)
const reason = ref('owner_request')
const detail = ref('')
const notify = ref(true)
const busy = ref(false)
const error = ref('')
const history = ref<ModerationEvent[]>([])
const valid = computed(() => restoring.value || (!!reason.value && (reason.value !== 'other' || !!detail.value.trim())))
const provider = computed(() => props.source.provider_name || props.source.provider_username || '管理员')
onMounted(async () => { try { history.value = (await moderationApi.history(props.source.id)).items } catch { history.value = [] } })
async function submit() {
  if (!valid.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const res = restoring.value ? await moderationApi.restore(props.source.id, notify.value) : await moderationApi.takedown(props.source.id, reason.value, detail.value.trim(), notify.value)
    toast(`${restoring.value ? '已恢复' : '已下架'}「${props.source.name}」${res.notified ? '，已邮件通知提供方' : ''}`)
    emit('done', res.source)
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <Modal :title="restoring ? '恢复来源' : '下架来源'" :busy="busy" @close="emit('close')">
    <form class="takedown-form" @submit.prevent="submit">
      <div class="takedown-target">
        <UserAvatar :id="source.owner_id" :name="provider" :version="source.provider_avatar" :size="40" />
        <div><strong>{{ source.name }}</strong><span class="small muted">提供方 {{ provider }}<template v-if="source.provider_username"> · @{{ source.provider_username }}</template> · {{ source.node_count }} 个节点</span></div>
      </div>
      <template v-if="restoring">
        <div class="takedown-current"><AppIcon name="alert" /><div><strong>{{ source.takedown!.reason_label }}</strong><p v-if="source.takedown!.detail">{{ source.takedown!.detail }}</p><span>{{ source.takedown!.by || '管理员' }} 于 {{ formatDate(source.takedown!.at) }} 下架</span></div></div>
        <p class="small muted">恢复后来源重新启用并排队刷新，节点会回到节点总览和订阅中。</p>
      </template>
      <template v-else>
        <div class="form-field"><span class="field-label">下架原因</span>
          <div class="reason-options" role="radiogroup" aria-label="下架原因">
            <label v-for="r in takedownReasons" :key="r.key" :class="['reason-option', { active: reason === r.key }]"><input v-model="reason" type="radio" name="takedown-reason" :value="r.key" /><span>{{ r.label }}</span></label>
          </div>
        </div>
        <label class="form-field"><span class="field-label">说明 <small class="muted">{{ reason === 'other' ? '必填' : '可选' }}</small></span><textarea v-model="detail" rows="3" maxlength="500" :placeholder="reason === 'owner_request' ? '例如：机场主于 10 月 17 日来信要求停止公开分享' : '会展示给提供方'"></textarea><span class="field-hint">{{ detail.length }}/500 · 提供方可在“共享来源”看到原因和说明。</span></label>
        <p class="notice warning small">下架后来源立即停用：节点从节点总览移除，所有订阅停止输出这些节点；提供方不能自行重新启用。</p>
      </template>
      <label class="checkbox-label"><input v-model="notify" type="checkbox" />邮件通知提供方（需对方已验证邮箱）</label>
      <details v-if="history.length" class="takedown-history"><summary>处理记录 {{ history.length }}</summary>
        <ol><li v-for="h in history" :key="h.id"><span :class="['mini-tag', h.action === 'takedown' ? 'red' : 'green']">{{ h.action === 'takedown' ? '下架' : '恢复' }}</span><span>{{ h.reason_label || '—' }}<template v-if="h.detail">：{{ h.detail }}</template></span><small>{{ h.operator }} · {{ formatDate(h.created_at) }}</small></li></ol>
      </details>
      <p v-if="error" class="notice error" role="alert">{{ error }}</p>
      <div class="dialog-actions"><button type="button" :disabled="busy" @click="emit('close')">取消</button><button :class="restoring ? 'primary' : 'danger-solid'" :disabled="busy || !valid"><span v-if="busy" class="spinner"></span>{{ restoring ? '恢复上架' : '确认下架' }}</button></div>
    </form>
  </Modal>
</template>
