<template>
  <div class="min-h-screen bg-background text-foreground">
    <div class="max-w-lg mx-auto px-3 py-5 sm:py-8 space-y-3">
      <div class="rounded-2xl bg-gradient-to-br from-brand-500 to-brand-600 text-white px-4 py-4">
        <div class="text-lg font-bold">房源登记</div>
        <div class="text-[12px] text-white/85 mt-0.5">
          {{ info?.ledger_name || '正在加载…' }}
        </div>
        <p class="text-[11px] text-white/80 mt-1.5 leading-5">
          把要出租的房屋信息填一下，登记后会由管理员核对；房屋信息进入台账后，
          有人来咨询空房时就能查到并推荐。
        </p>
      </div>

      <div v-if="loadError" class="surface rounded-2xl p-4 text-sm text-destructive">{{ loadError }}</div>
      <div v-else-if="!info" class="surface rounded-2xl p-6 text-center text-sm text-muted-foreground">加载中…</div>

      <template v-else-if="!done">
        <form class="surface rounded-2xl p-3.5 sm:p-4 space-y-3" @submit.prevent="submit">
          <div class="text-sm font-semibold">房屋信息</div>
          <label class="block">
            <span class="text-[11px] text-muted-foreground">小区 / 村组</span>
            <input v-model="form.community" class="input-field !py-2 mt-1" placeholder="如 幸福村 / 幸福小区" />
          </label>
          <div class="grid grid-cols-2 gap-2.5">
            <label class="block">
              <span class="text-[11px] text-muted-foreground">楼栋 / 院</span>
              <input v-model="form.building" class="input-field !py-2 mt-1" placeholder="如 3号院" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">单元</span>
              <input v-model="form.unit" class="input-field !py-2 mt-1" placeholder="选填" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">楼层</span>
              <input v-model="form.floor" class="input-field !py-2 mt-1" placeholder="如 2" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">房号 / 称呼 *</span>
              <input v-model="form.room_no" class="input-field !py-2 mt-1" placeholder="如 202 / 东厢房" required />
            </label>
          </div>
          <label class="block">
            <span class="text-[11px] text-muted-foreground">月租金（元）</span>
            <input v-model.number="form.default_rent" type="number" min="0" step="1" class="input-field !py-2 mt-1" placeholder="如 800" />
          </label>
          <label class="block">
            <span class="text-[11px] text-muted-foreground">备注</span>
            <input v-model="form.notes" class="input-field !py-2 mt-1" placeholder="如 家电齐全 / 可做小卖部 / 随时可看房" />
          </label>

          <!-- 介绍费：村民自己决定给不给介绍人、给多少（按平台规则） -->
          <div v-if="referral.policy !== 'off'" class="rounded-xl border border-border p-3 space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-semibold">介绍费（选填）</span>
              <span class="text-[11px] text-muted-foreground">
                {{ referral.policy === 'min' ? `平台规定最低 ${referral.min} 元` : '给推荐成交的人，线下结算' }}
              </span>
            </div>
            <div class="flex flex-wrap gap-1.5">
              <button
                v-for="opt in REFERRAL_MODES"
                :key="opt.value"
                type="button"
                class="px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors"
                :class="form.referral_mode === opt.value
                  ? 'bg-brand-500/15 border-brand-500/40 text-brand-600 dark:text-brand-300'
                  : 'bg-surface/70 border-border text-muted-foreground hover:bg-muted'"
                @click="form.referral_mode = opt.value"
              >{{ opt.label }}</button>
            </div>
            <label v-if="form.referral_mode !== ''" class="block">
              <span class="text-[11px] text-muted-foreground">
                {{ form.referral_mode === 'percent' ? '月租金的百分比（%）' : '金额（元）' }}
              </span>
              <input
                v-model.number="form.referral_value" type="number" step="0.01" min="0"
                class="input-field !py-2 mt-1"
                :placeholder="form.referral_mode === 'percent' ? '如 50' : '如 300'"
              />
            </label>
          </div>

          <div class="pt-1">
            <span class="text-[11px] text-muted-foreground">房屋照片（最多 {{ MAX_PHOTOS }} 张）</span>
            <div class="flex flex-wrap items-center gap-1.5 mt-1">
              <div v-for="(p, i) in photos" :key="i" class="relative">
                <img :src="p.url" class="w-14 h-14 rounded-lg object-cover border border-border" alt="房屋照片" />
                <button
                  type="button"
                  class="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-destructive text-white text-[11px] leading-none flex items-center justify-center shadow"
                  @click="removePhoto(i)"
                >×</button>
              </div>
              <button
                v-if="photos.length < MAX_PHOTOS"
                type="button"
                class="w-14 h-14 rounded-lg border-2 border-dashed border-border bg-muted/40 flex flex-col items-center justify-center gap-0.5 text-[10px] text-muted-foreground"
                @click="inputEl?.click()"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 9a2 2 0 012-2h.93a2 2 0 001.66-.9l.82-1.2A2 2 0 0110.07 4h3.86a2 2 0 011.66.9l.82 1.2a2 2 0 001.66.9H19a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V9z" />
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 13a3 3 0 11-6 0 3 3 0 016 0z" />
                </svg>
                <span>拍照</span>
              </button>
              <input ref="inputEl" type="file" accept="image/*" multiple class="hidden" @change="onPick" />
            </div>
          </div>

          <div class="pt-2 border-t border-border/60 space-y-2.5">
            <div class="text-sm font-semibold">您的联系方式（房东）</div>
            <div class="grid grid-cols-2 gap-2.5">
              <label class="block">
                <span class="text-[11px] text-muted-foreground">姓名 *</span>
                <input v-model="form.name" class="input-field !py-2 mt-1" placeholder="如 王村民" required />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">手机号 *</span>
                <input v-model="form.phone" class="input-field !py-2 mt-1" inputmode="tel" placeholder="便于管理员联系" required />
              </label>
            </div>
          </div>

          <div class="pt-2 border-t border-border/60 space-y-2.5">
            <div class="text-sm font-semibold">现有租户（选填）</div>
            <p class="text-[11px] text-muted-foreground">房子已经租出去了就填一下；空房可以留空。</p>
            <div class="grid grid-cols-2 gap-2.5">
              <label class="block">
                <span class="text-[11px] text-muted-foreground">租户姓名</span>
                <input v-model="form.tenant_name" class="input-field !py-2 mt-1" placeholder="选填" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">租户手机号</span>
                <input v-model="form.tenant_phone" class="input-field !py-2 mt-1" inputmode="tel" placeholder="选填" />
              </label>
            </div>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">租户身份证号</span>
              <input v-model="form.tenant_id_card" class="input-field !py-2 mt-1 tabular-nums" placeholder="选填，18 位或 15 位" />
            </label>
          </div>

          <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
          <button type="submit" class="btn-brand w-full !py-3" :disabled="submitting">
            {{ submitting ? '提交中…' : '提交登记' }}
          </button>
          <p class="text-[11px] text-muted-foreground text-center">
            提交后由管理员核对，通过后房屋进入台账；您的手机号只用于联系核实。
          </p>
        </form>
      </template>

      <div v-else class="surface rounded-2xl p-6 text-center space-y-2">
        <div class="text-2xl">✅</div>
        <div class="text-base font-semibold">已提交，等待核对</div>
        <p class="text-xs text-muted-foreground leading-5">
          房屋信息已送到{{ info?.ledger_name || '管理员' }}的待审列表，核对后会建入台账。
          如需补充或修改，请联系管理员。
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'

import { getPublicConfigs } from '../api/config'
import { getPublicRoomApplyInfo, submitPublicRoomApply } from '../api/rental'
import { parseReferralSettings, type ReferralSettings } from '../utils/referral'

// 免登录的房源登记页（村委/中介场景）：村民扫码填房源（可选带上现有租户），
// 提交进台账的「待审房源」队列。公开路由，不需要登录态，也不带台账参数。
const MAX_PHOTOS = 3

const route = useRoute()
const token = String(route.params.token || '')

const info = ref<{ ledger_name?: string } | null>(null)
const loadError = ref('')
const error = ref('')
const submitting = ref(false)
const done = ref(false)
const inputEl = ref<HTMLInputElement | null>(null)
const photos = ref<{ file: File; url: string }[]>([])

const form = reactive({
  community: '', building: '', unit: '', floor: '', room_no: '',
  default_rent: 0 as number | null, notes: '',
  name: '', phone: '',
  tenant_name: '', tenant_phone: '', tenant_id_card: '',
  referral_mode: '' as '' | 'fixed' | 'percent', referral_value: 0,
})

const REFERRAL_MODES: { value: '' | 'fixed' | 'percent'; label: string }[] = [
  { value: '', label: '不给介绍费' },
  { value: 'fixed', label: '固定金额' },
  { value: 'percent', label: '月租金百分比' },
]

// 介绍费规则（公开配置）：off 时不显示该区块
const referral = ref<ReferralSettings>({ policy: 'free', min: 200 })
getPublicConfigs()
  .then((res) => { if (res.data?.code === 0) referral.value = parseReferralSettings(res.data.data) })
  .catch(() => {})

getPublicRoomApplyInfo(token)
  .then((res) => {
    if (res.data?.code === 0) info.value = res.data.data
    else loadError.value = res.data?.message || '登记链接无效'
  })
  .catch((err) => {
    loadError.value = err.response?.data?.message || '登记链接无效或已被重置，请联系分享链接的人'
  })

function onPick(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  for (const f of files) {
    if (photos.value.length >= MAX_PHOTOS) break
    if (f.size > 10 * 1024 * 1024) {
      error.value = '单张照片需在 10MB 以内'
      break
    }
    photos.value.push({ file: f, url: URL.createObjectURL(f) })
  }
}

function removePhoto(i: number) {
  URL.revokeObjectURL(photos.value[i].url)
  photos.value.splice(i, 1)
}

async function submit() {
  error.value = ''
  if (!form.name.trim()) {
    error.value = '请填写您的姓名'
    return
  }
  if (!/^[0-9+\- ]{5,20}$/.test(form.phone.trim())) {
    error.value = '请填写正确的手机号'
    return
  }
  if (!form.room_no.trim()) {
    error.value = '请填写房号或房屋称呼'
    return
  }
  submitting.value = true
  try {
    const fd = new FormData()
    fd.append('name', form.name.trim())
    fd.append('phone', form.phone.trim())
    fd.append('community', form.community.trim())
    fd.append('building', form.building.trim())
    fd.append('unit', form.unit.trim())
    fd.append('floor', form.floor.trim())
    fd.append('room_no', form.room_no.trim())
    fd.append('default_rent', String(form.default_rent ?? 0))
    fd.append('notes', form.notes.trim())
    fd.append('tenant_name', form.tenant_name.trim())
    fd.append('tenant_phone', form.tenant_phone.trim())
    fd.append('tenant_id_card', form.tenant_id_card.trim().toUpperCase())
    fd.append('referral_mode', form.referral_mode)
    fd.append('referral_value', String(form.referral_value ?? 0))
    for (const p of photos.value) fd.append('photos', p.file)
    const res = await submitPublicRoomApply(token, fd)
    if (res.data?.code === 0) {
      done.value = true
      for (const p of photos.value) URL.revokeObjectURL(p.url)
    } else {
      error.value = res.data?.message || '提交失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '提交失败，请检查网络后重试'
  } finally {
    submitting.value = false
  }
}

onBeforeUnmount(() => {
  for (const p of photos.value) URL.revokeObjectURL(p.url)
})
</script>
