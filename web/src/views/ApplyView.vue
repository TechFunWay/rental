<template>
  <div class="min-h-dvh bg-muted/40 flex flex-col items-center px-4 py-6">
    <div class="w-full max-w-md">
      <!-- 顶部：房源信息 -->
      <div class="surface rounded-2xl p-4 mb-4 flex items-center gap-3">
        <div class="w-11 h-11 rounded-xl bg-brand-500/10 text-brand-600 dark:text-brand-300 flex items-center justify-center text-xl shrink-0">🏠</div>
        <div class="min-w-0">
          <div class="font-semibold text-foreground">{{ linkInvalid ? '登记链接无效' : roomTitle }}</div>
          <div class="text-xs text-muted-foreground mt-0.5">
            {{ linkInvalid ? linkError : '入住信息登记 · 资料只提供给本房源房东' }}
          </div>
        </div>
      </div>

      <!-- 链接失效：直接给结论，不留可填可提交的表单 -->
      <div v-if="linkInvalid" class="surface rounded-2xl p-4 text-center space-y-2">
        <div class="text-3xl">🔗</div>
        <p class="text-sm text-foreground font-semibold">这个登记链接已失效</p>
        <p class="text-xs text-muted-foreground leading-5">{{ linkError }}</p>
      </div>

      <template v-else-if="!submitted">
        <div class="surface rounded-2xl p-4 space-y-4">
          <!-- 第一步：身份证照片 -->
          <div>
            <div class="text-xs font-semibold text-muted-foreground mb-1.5 flex items-center gap-1.5">
              <span class="step-dot">1</span> 拍摄 / 上传身份证人像面
              <span class="font-normal text-muted-foreground">（选填，有照片房东更好核对）</span>
            </div>
            <label class="block cursor-pointer">
              <input type="file" accept="image/*" capture="environment" class="hidden" @change="onPickPhoto" />
              <div v-if="!photoUrl" class="rounded-xl border-2 border-dashed border-border py-7 text-center text-sm text-muted-foreground active:bg-muted/50 transition-colors">
                📷 点击拍照或从相册选择
                <div class="text-[11px] mt-1">照片仅房东可见，用于核对身份</div>
              </div>
              <img v-else :src="photoUrl" class="w-full rounded-xl border border-border" alt="身份证照片预览" />
            </label>
            <div v-if="photoUrl" class="flex items-center gap-2 mt-2">
              <button type="button" class="btn-ghost !py-1.5 !px-3 text-xs flex-1" @click="clearPhoto">重选照片</button>
              <button type="button" class="btn-brand !py-1.5 !px-3 text-xs flex-1" :disabled="ocrLoading" @click="runOCR">
                {{ ocrLoading ? '识别中…' : '✨ AI 自动识别' }}
              </button>
            </div>
            <p v-if="ocrHint" class="text-[11px] mt-1.5" :class="ocrOk ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'">{{ ocrHint }}</p>
          </div>

          <!-- 第二步：核对/填写信息 -->
          <div>
            <div class="text-xs font-semibold text-muted-foreground mb-1.5 flex items-center gap-1.5">
              <span class="step-dot">2</span> 核对信息（识别后可改正）
            </div>
            <div class="space-y-2.5">
              <div>
                <label class="text-[11px] text-muted-foreground block mb-1">姓名 *</label>
                <input v-model="form.name" class="input-field !py-2" placeholder="身份证上的姓名" />
              </div>
              <div>
                <label class="text-[11px] text-muted-foreground block mb-1">身份证号</label>
                <input v-model="form.idCard" class="input-field !py-2 tabular-nums" placeholder="选填，18 位" />
              </div>
              <div>
                <label class="text-[11px] text-muted-foreground block mb-1">身份证住址</label>
                <input v-model="form.address" class="input-field !py-2" placeholder="选填，证件上的住址" />
              </div>
            </div>
          </div>

          <!-- 第三步：手机号 -->
          <div>
            <div class="text-xs font-semibold text-muted-foreground mb-1.5 flex items-center gap-1.5">
              <span class="step-dot">3</span> 联系手机号 *
            </div>
            <input v-model="form.phone" type="tel" class="input-field !py-2 tabular-nums" placeholder="房东将通过这个号码联系你" />
          </div>

          <div>
            <label class="text-[11px] text-muted-foreground block mb-1">给房东留言（选填）</label>
            <input v-model="form.remark" class="input-field !py-2" placeholder="如入住时间、同住人数" />
          </div>

          <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
          <button type="button" class="btn-brand w-full !py-2.5" :disabled="submitting || !canSubmit" @click="submit">
            {{ submitting ? '提交中…' : '提交给房东' }}
          </button>
          <p class="text-[11px] text-muted-foreground text-center leading-relaxed">
            提交后房东会核对信息并联系你办理入住；资料仅本房源房东可见。
          </p>
        </div>
      </template>

      <!-- 提交成功 -->
      <div v-else class="surface rounded-2xl p-8 text-center">
        <div class="text-4xl mb-3">✅</div>
        <p class="font-semibold text-foreground">已提交，请等待房东确认</p>
        <p class="text-sm text-muted-foreground mt-2 leading-relaxed">
          房东核对后会联系你办理入住。<br />保持手机畅通即可，无需重复提交。
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// 租客自助登记页（公开）：凭房源 token 收集身份证照片与联系方式。
// 拍照后本地压成 JPEG（iPhone 直出 HEIC 大多数识别接口不认，且原图动辄 5MB+），
// AI 识别是加分项：未配置/失败都退化为手填，不挡提交。
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { getApplyInfo, ocrIdCard, submitApply } from '../api/rental'

const route = useRoute()
const token = String(route.params.token || '')

const roomTitle = ref('入住信息登记')
const submitted = ref(false)
const submitting = ref(false)
const error = ref('')

const photoFile = ref<File | null>(null)
const photoUrl = ref('')
const ocrLoading = ref(false)
const ocrHint = ref('')
const ocrOk = ref(false)

const form = ref({ name: '', phone: '', idCard: '', address: '', remark: '' })
// linkInvalid 为真时整页只提示重拿链接：此前失效链接仍能填完整张表、点提交才报错
// （2026-10-11 审计），而且提示文案被 truncate 截断成「登记链接无效或已失…」。
const linkInvalid = ref(false)
const linkError = ref('')
const canSubmit = computed(() =>
  !linkInvalid.value && form.value.name.trim() !== '' && form.value.phone.trim() !== '')

getApplyInfo(token).then((res) => {
  if (res.data?.code === 0 && res.data.data) {
    const d = res.data.data
    const parts = [d.community, d.building, d.unit, d.floor ? d.floor + '层' : '', d.room_no]
    roomTitle.value = parts.filter(Boolean).join(' ') || '入住信息登记'
  } else {
    linkInvalid.value = true
    linkError.value = res.data?.message || '登记链接无效'
    roomTitle.value = '登记链接无效'
  }
}).catch(() => {
  linkInvalid.value = true
  linkError.value = '登记链接无效或已失效，请联系房东重新分享'
  roomTitle.value = '登记链接无效'
})

// 选照片：本地解码 → 长边压到 1600px → JPEG（兼容 iPhone HEIC，也减小上传体积）
async function onPickPhoto(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    photoFile.value = await compressToJpeg(file)
    // 重选照片：先释放上一张的 blob URL，否则反复选图会一直占内存。
    if (photoUrl.value) URL.revokeObjectURL(photoUrl.value)
    photoUrl.value = URL.createObjectURL(photoFile.value)
    ocrHint.value = ''
  } catch {
    error.value = '照片读取失败，请换一张试试'
  }
}

function clearPhoto() {
  photoFile.value = null
  if (photoUrl.value) URL.revokeObjectURL(photoUrl.value)
  photoUrl.value = ''
  ocrHint.value = ''
}

async function compressToJpeg(file: File): Promise<File> {
  try {
    const bitmap = await createImageBitmap(file)
    const maxSide = 1600
    const scale = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height))
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(bitmap.width * scale)
    canvas.height = Math.round(bitmap.height * scale)
    canvas.getContext('2d')?.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.85))
    bitmap.close()
    if (blob) return new File([blob], 'id-card.jpg', { type: 'image/jpeg' })
  } catch { /* 解码失败（如不支持的格式）就按原文件传，由服务端校验 */ }
  return file
}

async function runOCR() {
  if (!photoFile.value || ocrLoading.value) return
  ocrLoading.value = true
  ocrHint.value = ''
  try {
    const fd = new FormData()
    fd.append('photo', photoFile.value)
    const res = await ocrIdCard(token, fd)
    if (res.data?.code === 0 && res.data.data) {
      const d = res.data.data
      if (d.name) form.value.name = d.name
      if (d.id_card) form.value.idCard = d.id_card
      if (d.address) form.value.address = d.address
      ocrOk.value = true
      ocrHint.value = '识别完成，请核对下方信息（可修改）'
    } else {
      ocrOk.value = false
      ocrHint.value = res.data?.message || '识别失败，请手动填写'
    }
  } catch {
    ocrOk.value = false
    ocrHint.value = '识别服务连接失败，请手动填写'
  } finally {
    ocrLoading.value = false
  }
}

async function submit() {
  if (!canSubmit.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    const fd = new FormData()
    fd.append('name', form.value.name.trim())
    fd.append('phone', form.value.phone.trim())
    if (form.value.idCard.trim()) fd.append('id_card', form.value.idCard.trim())
    if (form.value.address.trim()) fd.append('address', form.value.address.trim())
    if (form.value.remark.trim()) fd.append('remark', form.value.remark.trim())
    if (photoFile.value) fd.append('photo', photoFile.value)
    const res = await submitApply(token, fd)
    if (res.data?.code === 0) {
      submitted.value = true
    } else {
      error.value = res.data?.message || '提交失败，请稍后再试'
    }
  } catch {
    error.value = '网络异常，请稍后再试'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.step-dot {
  @apply inline-flex w-4 h-4 rounded-full bg-brand-500/15 text-brand-600 dark:text-brand-300 items-center justify-center text-[10px] font-bold;
}
</style>
