<template>
  <Modal :model-value="modelValue" title="移动到共同台账" @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3">
      <div class="rounded-xl border border-brand-500/30 bg-brand-500/[0.06] px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        把
        <span class="font-semibold text-foreground">{{ room?.room_no || '该房源' }}</span>
        连同它的<span class="font-semibold text-foreground">全部数据</span>一起移到另一个共同台账：租户与入住成员、抄表记录与照片、
        账单与收款、合同与附件、房间照片、待审入住登记。移动后当前台账不再显示这间房。
      </div>

      <div v-if="targets.length === 0" class="rounded-xl border border-border px-3 py-3 text-xs text-muted-foreground leading-5">
        没有可选的共同台账。要移动到这里，你需要在某个共同台账里拥有「房源」模块的
        <span class="text-foreground">操作权限</span>；还没有共同台账的话，先在左侧的台账
        切换器里新建一个。
      </div>

      <template v-else>
        <div>
          <span class="text-xs font-semibold text-muted-foreground block mb-1.5">目标台账（只列你有操作权限的共同台账）</span>
          <div class="space-y-1.5">
            <button
              v-for="l in targets"
              :key="l.id"
              type="button"
              class="w-full text-left rounded-xl border px-3 py-2.5 transition-colors"
              :class="selectedId === l.id
                ? 'border-brand-500/60 bg-brand-500/[0.08]'
                : 'border-border bg-surface/80 hover:bg-muted/60'"
              @click="selectedId = l.id"
            >
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs font-semibold text-foreground truncate">{{ l.name }}</span>
                <span v-if="selectedId === l.id" class="text-[11px] font-semibold text-brand-600 dark:text-brand-300 shrink-0">已选中</span>
              </div>
              <div class="mt-0.5 text-[11px] text-muted-foreground">
                共同台账<template v-if="l.owner_name"> · 管理者 {{ l.owner_name }}</template>
                <template v-if="l.member_count"> · {{ l.member_count }} 位成员</template>
              </div>
            </button>
          </div>
        </div>

        <p class="text-[11px] text-muted-foreground leading-5">
          这是<span class="text-foreground font-medium">归属迁移</span>：旧台账的房间数、账单与收款统计会随之减少，新台账会增加。
          迁移前建议先做一次备份（系统管理 → 备份管理）。
        </p>

        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" :disabled="busy" @click="emit('update:modelValue', false)">取消</button>
          <button
            type="button"
            class="flex-1 rounded-xl px-4 py-2.5 text-sm font-semibold text-white bg-brand-500 hover:bg-brand-600 transition-colors disabled:opacity-60"
            :disabled="busy || !selectedId"
            @click="confirmMove"
          >{{ busy ? '移动中…' : '移动到该台账' }}</button>
        </div>
      </template>

      <div v-if="targets.length === 0" class="flex pt-1">
        <button class="btn-ghost flex-1" @click="emit('update:modelValue', false)">关闭</button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Modal from './Modal.vue'
import { useLedgerStore } from '../stores/ledger'
import { toast } from '../utils/toast'
import { PERM_RANK } from '../utils/perm'
import { moveRoom, type RoomView } from '../api/rental'

// 房源跨台账移动（2026-10-13 用户要求「自己权限下的房源，可以移动到任何一个自己
// 有权限的共同台账下面」）。目标只列：共同台账 + 非当前台账 + 我在该台账有「房源」
// 操作档——与后端判定口径一致（源台账要完全档，入口在列表里已按 canFull 控制）。
const props = defineProps<{
  modelValue: boolean
  room: RoomView | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 迁移成功后通知列表刷新 */
  moved: []
}>()

const ledgerStore = useLedgerStore()
const selectedId = ref(0)
const busy = ref(false)
const error = ref('')

const targets = computed(() =>
  ledgerStore.ledgers.filter((l) =>
    l.kind === 'shared' &&
    l.id !== ledgerStore.currentLedgerId &&
    (PERM_RANK[l.perms?.rooms as keyof typeof PERM_RANK] ?? 0) >= PERM_RANK.edit,
  ),
)

async function confirmMove() {
  const room = props.room
  if (!room || !selectedId.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await moveRoom(room.id, selectedId.value)
    if (res.data?.code === 0) {
      const counts = res.data.data?.counts ?? {}
      const parts = [
        counts.tenants ? `租户 ${counts.tenants}` : '',
        counts.members ? `入住成员 ${counts.members}` : '',
        counts.bills ? `账单 ${counts.bills}` : '',
        counts.meter_records ? `抄表 ${counts.meter_records}` : '',
        (counts.contracts || counts.attachments || counts.room_photos || counts.meter_photos)
          ? `附件 ${(counts.contracts ?? 0) + (counts.attachments ?? 0) + (counts.room_photos ?? 0) + (counts.meter_photos ?? 0)}`
          : '',
      ].filter(Boolean).join(' · ')
      toast(parts ? `已移动：${parts}` : '已移动到目标台账')
      emit('moved')
      emit('update:modelValue', false)
    } else {
      error.value = res.data?.message || '移动失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '移动失败，请重试'
  } finally {
    busy.value = false
  }
}

watch(() => [props.modelValue, props.room?.id] as const, ([open]) => {
  if (!open) return
  selectedId.value = targets.value[0]?.id ?? 0
  error.value = ''
}, { immediate: true })
</script>
