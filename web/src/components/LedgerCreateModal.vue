<template>
  <Modal v-model="visible" title="新建共同台账">
    <div class="space-y-4">
      <div class="rounded-2xl bg-muted/60 px-4 py-3 text-xs leading-5 text-muted-foreground">
        共同台账可以多人一起管：你是<strong class="text-foreground">管理者</strong>，能邀请成员并给每个人勾选权限。
        台账之间的数据不互通，你自己原来的房源账单留在「我的台账」里不受影响。
      </div>

      <div>
        <label class="block text-sm font-medium text-foreground mb-1.5">台账名称</label>
        <input
          v-model="name"
          class="input-field"
          maxlength="50"
          placeholder="例如：A区合租、老家房子"
          @keyup.enter="submit"
        />
      </div>

      <div>
        <label class="block text-sm font-medium text-foreground mb-1.5">邀请成员（可留空）</label>
        <textarea
          v-model="usernames"
          class="input-field min-h-[76px]"
          placeholder="填写已注册的用户名，多个用逗号或换行分隔"
        ></textarea>
        <p class="mt-1.5 text-xs text-muted-foreground">
          对方会收到邀请，接受后才算成员；加入后默认只有查看权限，你可以随时在「成员与权限」里调整。
        </p>
      </div>

      <p v-if="errorMsg" class="text-sm text-destructive">{{ errorMsg }}</p>

      <div class="flex items-center gap-3 pt-1">
        <button type="button" class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold text-muted-foreground hover:bg-muted transition-colors" @click="close">取消</button>
        <button type="button" class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold bg-brand-500 text-white hover:bg-brand-600 transition-colors disabled:opacity-60" :disabled="submitting" @click="submit">
          {{ submitting ? '创建中…' : '创建台账' }}
        </button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
// 新建共同台账：名称 + 可选的一串用户名（建台账时直接发出邀请）。
import { ref, watch } from 'vue'
import Modal from './Modal.vue'
import { createLedger } from '../api/ledger'
import { toast } from '../utils/toast'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  created: [ledgerId: number]
}>()

const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v })
watch(visible, (v) => emit('update:modelValue', v))

const name = ref('')
const usernames = ref('')
const submitting = ref(false)
const errorMsg = ref('')

function close() {
  visible.value = false
}

async function submit() {
  errorMsg.value = ''
  const trimmed = name.value.trim()
  if (!trimmed) {
    errorMsg.value = '请填写台账名称'
    return
  }
  submitting.value = true
  try {
    const res = await createLedger(trimmed, usernames.value)
    if (res.data?.code === 0) {
      const data = res.data.data
      if (data?.skipped?.length) {
        toast(`台账已创建；这些用户名跳过：${data.skipped.join('、')}`, 'error')
      }
      name.value = ''
      usernames.value = ''
      visible.value = false
      if (data?.id) emit('created', data.id)
    } else {
      errorMsg.value = res.data?.message || '创建失败'
    }
  } catch (err: any) {
    errorMsg.value = err?.response?.data?.message || '网络错误'
  } finally {
    submitting.value = false
  }
}
</script>
