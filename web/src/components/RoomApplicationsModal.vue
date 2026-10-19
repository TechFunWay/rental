<template>
  <Modal :model-value="modelValue" title="待审房源" wide @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3">
      <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        村民/中介通过「房源登记链接」提交的房屋，核对后点「通过」即建入台账
        （房源可被抄表开票、可生成租客登记链接）；信息不对就驳回并写明原因。
        随登记提交了现有租户的，通过时一并建档。
      </div>

      <div v-if="loading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
      <div v-else-if="items.length === 0" class="py-6 text-center text-sm text-muted-foreground">
        没有待审的房源登记。把「房源登记链接」发到村民群或村委公告栏，村民扫码提交后会出现在这里。
      </div>
      <div v-else class="space-y-2.5">
        <div v-for="a in items" :key="a.id" class="rounded-xl border border-border p-3 space-y-2">
          <div class="flex items-start justify-between gap-2 flex-wrap">
            <div class="min-w-0">
              <div class="text-sm font-semibold text-foreground">
                {{ a.room_no }}
                <span class="text-xs font-normal text-muted-foreground ml-1">
                  {{ [a.community, a.building, a.unit, a.floor ? a.floor + '层' : ''].filter(Boolean).join(' ') || '未填位置' }}
                </span>
              </div>
              <div class="text-[11px] text-muted-foreground mt-0.5">
                月租金 {{ fmtMoney(a.default_rent) }} 元 · 提交人 {{ a.name }}
                <span class="tabular-nums">{{ a.phone }}</span>
              </div>
            </div>
            <span class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-amber-500/10 text-amber-600 dark:text-amber-300">待审核</span>
          </div>

          <div v-if="a.notes" class="text-[11px] text-muted-foreground">备注：{{ a.notes }}</div>
          <div v-if="referralText(a.referral_mode || '', a.referral_value || 0, a.default_rent || 0)" class="text-[11px] text-amber-600 dark:text-amber-300">
            登记人挂了 {{ referralText(a.referral_mode || '', a.referral_value || 0, a.default_rent || 0) }}（审核通过后写入房源）
          </div>
          <div v-if="a.tenant_name" class="text-[11px] text-muted-foreground">
            现有租户：<span class="text-foreground">{{ a.tenant_name }}</span>
            <span v-if="a.tenant_phone" class="tabular-nums"> · {{ a.tenant_phone }}</span>
            <span v-if="a.tenant_id_card" class="tabular-nums"> · {{ a.tenant_id_card }}</span>
          </div>

          <div>
            <button type="button" class="text-[11px] text-brand-600 dark:text-brand-300 hover:underline" @click="togglePhotos(a)">
              {{ expanded[a.id] ? '收起照片' : `查看照片（${a.photo_urls.length}）` }}
            </button>
            <div v-if="expanded[a.id]" class="flex flex-wrap gap-1.5 mt-1.5">
              <img
                v-for="(p, i) in photos[a.id] || []"
                :key="i"
                :src="p"
                class="w-16 h-16 rounded-lg object-cover border border-border"
                alt="房屋照片"
              />
              <span v-if="(photos[a.id] || []).length === 0" class="text-[11px] text-muted-foreground">加载中…</span>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-2">
            <label class="block">
              <span class="text-[11px] text-muted-foreground">房号（可改）</span>
              <input v-model="draft[a.id].room_no" class="input-field !py-1.5 text-xs mt-0.5" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">月租金（可改）</span>
              <input v-model.number="draft[a.id].default_rent" type="number" min="0" step="1" class="input-field !py-1.5 text-xs mt-0.5" />
            </label>
          </div>

          <div v-if="rejecting[a.id]" class="space-y-1.5">
            <input v-model="rejectNote[a.id]" class="input-field !py-1.5 text-xs" placeholder="驳回原因，如「电话打不通 / 房号重复」" />
          </div>

          <div class="flex items-center gap-2 flex-wrap">
            <button
              type="button"
              class="btn-brand !py-1.5 !px-3 text-xs"
              :disabled="busy[a.id]"
              @click="approve(a)"
            >{{ busy[a.id] ? '处理中…' : '通过并建入台账' }}</button>
            <template v-if="!rejecting[a.id]">
              <button type="button" class="text-xs text-destructive hover:underline" @click="rejecting[a.id] = true">驳回</button>
            </template>
            <template v-else>
              <button type="button" class="text-xs text-destructive font-medium" :disabled="busy[a.id]" @click="reject(a)">确认驳回</button>
              <button type="button" class="text-xs text-muted-foreground" @click="rejecting[a.id] = false">取消</button>
            </template>
          </div>
        </div>
      </div>

      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <div class="flex gap-3 pt-1">
        <button class="btn-ghost flex-1" @click="emit('update:modelValue', false)">关闭</button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'

import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import {
  approveRoomApplication, fetchRoomApplicationPhotoBlob, getRoomApplications, rejectRoomApplication,
  fmtMoney, type RoomApplication,
} from '../api/rental'
import { referralText } from '../utils/referral'

// 待审房源队列（村委/中介场景）：村民免登录提交的房屋在这里核对、通过即建入台账。
const props = defineProps<{ modelValue: boolean }>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 审核后有变化（建房源 / 驳回），通知列表刷新与角标更新 */
  changed: []
}>()

const loading = ref(false)
const error = ref('')
const items = ref<RoomApplication[]>([])
const expanded = reactive<Record<number, boolean>>({})
const photos = reactive<Record<number, string[]>>({})
const draft = reactive<Record<number, { room_no: string; default_rent: number }>>({})
const rejecting = reactive<Record<number, boolean>>({})
const rejectNote = reactive<Record<number, string>>({})
const busy = reactive<Record<number, boolean>>({})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await getRoomApplications({ status: 'pending' })
    if (res.data?.code === 0) {
      items.value = res.data.data ?? []
      for (const a of items.value) {
        draft[a.id] = { room_no: a.room_no, default_rent: a.default_rent }
        rejecting[a.id] = false
        rejectNote[a.id] = ''
      }
    } else {
      error.value = res.data?.message || '加载待审房源失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '加载待审房源失败，请重试'
  } finally {
    loading.value = false
  }
}

// 照片按需加载（走登录接口取流再转 blob），列表很长时不必一次拉几百张
// 回到未展开时把已建的地址回收（组件常驻，只靠卸载回收会一路累积）
function releasePhotos(a: RoomApplication) {
  for (const url of photos[a.id] || []) URL.revokeObjectURL(url)
  photos[a.id] = []
}

async function togglePhotos(a: RoomApplication) {
  expanded[a.id] = !expanded[a.id]
  if (!expanded[a.id]) {
    releasePhotos(a)
    return
  }
  if (photos[a.id]) return
  photos[a.id] = []
  for (const url of a.photo_urls) {
    try {
      const blob = await fetchRoomApplicationPhotoBlob(url)
      photos[a.id].push(URL.createObjectURL(blob))
    } catch {
      // 单张失败不影响其余
    }
  }
}

async function approve(a: RoomApplication) {
  busy[a.id] = true
  error.value = ''
  try {
    const res = await approveRoomApplication(a.id, draft[a.id])
    if (res.data?.code === 0) {
      const d = res.data.data ?? {}
      toast(`已建入台账：${d.room_label || draft[a.id].room_no}${d.tenant_id ? '（含租户）' : ''}`)
      await load()
      emit('changed')
    } else {
      error.value = res.data?.message || '审核失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '审核失败，请重试'
  } finally {
    busy[a.id] = false
  }
}

async function reject(a: RoomApplication) {
  busy[a.id] = true
  error.value = ''
  try {
    const res = await rejectRoomApplication(a.id, rejectNote[a.id] || '')
    if (res.data?.code === 0) {
      toast('已驳回')
      await load()
      emit('changed')
    } else {
      error.value = res.data?.message || '驳回失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '驳回失败，请重试'
  } finally {
    busy[a.id] = false
  }
}

watch(() => props.modelValue, (open) => {
  if (!open) {
    // 关窗时清掉照片地址与展开状态，下次打开重新按需加载
    for (const a of items.value) releasePhotos(a)
    for (const key of Object.keys(expanded)) expanded[Number(key)] = false
    return
  }
  void load()
}, { immediate: true })

onBeforeUnmount(() => {
  for (const a of items.value) releasePhotos(a)
})
</script>
