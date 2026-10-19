<template>
  <Modal :model-value="modelValue" title="申请修改登记信息" @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3.5">
      <!-- 说明：为什么要审核、哪些能改 -->
      <div class="rounded-xl border border-brand-500/30 bg-brand-500/[0.06] px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        修改后需要房东审核，通过才会更新到正式资料；审核前你看到的一直是原信息。
        <span class="text-foreground font-medium">姓名、房间、租金、租期与押金由房东维护，不能在这里修改。</span>
      </div>

      <div>
        <label class="block text-xs font-semibold text-muted-foreground mb-1.5">手机号</label>
        <input v-model="form.phone" class="input-field" inputmode="tel" placeholder="留空表示本次不改" />
      </div>

      <div>
        <label class="block text-xs font-semibold text-muted-foreground mb-1.5">身份证号</label>
        <input v-model="form.id_card" class="input-field" placeholder="留空表示本次不改" />
      </div>

      <div>
        <label class="block text-xs font-semibold text-muted-foreground mb-1.5">身份证住址</label>
        <input v-model="form.address" class="input-field" placeholder="留空表示本次不改" />
      </div>

      <div class="grid grid-cols-2 gap-2.5">
        <div>
          <label class="block text-xs font-semibold text-muted-foreground mb-1.5">紧急联系人</label>
          <input v-model="form.emergency_contact" class="input-field" placeholder="留空不改" />
        </div>
        <div>
          <label class="block text-xs font-semibold text-muted-foreground mb-1.5">紧急联系电话</label>
          <input v-model="form.emergency_phone" class="input-field" inputmode="tel" placeholder="留空不改" />
        </div>
      </div>

      <div>
        <label class="block text-xs font-semibold text-muted-foreground mb-1.5">留言给房东（选填）</label>
        <textarea v-model="form.note" class="input-field" rows="2" placeholder="如：换了手机号，请更新一下" />
      </div>

      <div>
        <label class="block text-xs font-semibold text-muted-foreground mb-1.5">
          证件照（选填，最多 {{ MAX_PHOTOS }} 张）
        </label>
        <!-- 已选文件列表：手机端一屏内可见，可逐个移除 -->
        <div v-if="photos.length" class="space-y-1.5 mb-2">
          <div
            v-for="(p, i) in photos"
            :key="`${p.name}-${i}`"
            class="flex items-center gap-2 rounded-xl border border-border bg-surface/80 px-3 py-2"
          >
            <span class="text-[11px] font-semibold text-muted-foreground shrink-0">{{ i + 1 }}</span>
            <span class="text-xs text-foreground truncate flex-1 min-w-0">{{ p.name }}</span>
            <button type="button" class="text-xs text-destructive shrink-0 px-1.5 py-1" @click="removePhoto(i)">移除</button>
          </div>
        </div>
        <FilePick
          v-if="photos.length < MAX_PHOTOS"
          accept="image/*,application/pdf"
          multiple
          label="拍照或选择证件照"
          hint="支持图片与 PDF，单张不超过 10MB"
          @change="onPick"
        />
      </div>
    </div>

    <div class="flex items-center gap-3 pt-4">
      <button type="button" class="btn-ghost flex-1" :disabled="submitting" @click="emit('update:modelValue', false)">取消</button>
      <button type="button" class="btn-brand flex-1" :disabled="submitting || !hasChange" @click="submit">
        {{ submitting ? '提交中…' : '提交申请' }}
      </button>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Modal from '../Modal.vue'
import FilePick from '../FilePick.vue'
import { submitChangeRequest } from '../../api/tenant'
import { toast } from '../../utils/toast'
import { serverErrorMessage } from '../../utils/apiError'

const props = defineProps<{
  modelValue: boolean
  /** 当前正式值：打开弹窗时预填，方便租户在原有基础上改一两项。 */
  initial?: {
    phone?: string
    id_card?: string
    address?: string
    emergency_contact?: string
    emergency_phone?: string
  }
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submitted: []
}>()

// MAX_PHOTOS 与后端 changePhotoMax 一致（见 server/rental/tenant_portal.go）。
const MAX_PHOTOS = 3

const form = reactive({
  phone: '',
  id_card: '',
  address: '',
  emergency_contact: '',
  emergency_phone: '',
  note: '',
})
const photos = ref<File[]>([])
const submitting = ref(false)

// 打开时预填当前值（证件号服务端回的是脱敏值，若带 * 就不预填——
// 否则提交上去会把脱敏串当成新证件号，反而改坏了正式数据）。
watch(() => props.modelValue, (open) => {
  if (!open) return
  form.phone = props.initial?.phone || ''
  form.id_card = (props.initial?.id_card || '').includes('*') ? '' : (props.initial?.id_card || '')
  form.address = props.initial?.address || ''
  form.emergency_contact = props.initial?.emergency_contact || ''
  form.emergency_phone = props.initial?.emergency_phone || ''
  form.note = ''
  photos.value = []
})

// 至少要有一样内容才允许提交（后端也会校验，前端先给即时反馈）。
const hasChange = computed(() =>
  !!form.phone || !!form.id_card || !!form.address ||
  !!form.emergency_contact || !!form.emergency_phone || photos.value.length > 0,
)

function onPick(files: File[]) {
  const room = MAX_PHOTOS - photos.value.length
  if (room <= 0) {
    toast(`最多上传 ${MAX_PHOTOS} 张照片`, 'error')
    return
  }
  photos.value = [...photos.value, ...files.slice(0, room)]
  if (files.length > room) toast(`最多上传 ${MAX_PHOTOS} 张，已保留前 ${room} 张`, 'error')
}

function removePhoto(i: number) {
  photos.value = photos.value.filter((_, idx) => idx !== i)
}

async function submit() {
  if (!hasChange.value) {
    toast('请至少填写一项要修改的内容', 'error')
    return
  }
  submitting.value = true
  try {
    const res = await submitChangeRequest({ ...form }, photos.value)
    if (res.data?.code === 0) {
      toast('已提交，等待房东审核')
      emit('update:modelValue', false)
      emit('submitted')
    } else {
      toast(res.data?.message || '提交失败', 'error')
    }
  } catch (e) {
    // 服务端业务提示（如重复提交、格式错误）优先透出，网络异常才用兜底文案。
    toast(serverErrorMessage(e, '提交失败，请稍后再试'), 'error')
  } finally {
    submitting.value = false
  }
}
</script>
