<template>
  <Modal :model-value="modelValue" title="关联登录账号" @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3.5">
      <div class="rounded-xl border border-brand-500/30 bg-brand-500/[0.06] px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        租客自己注册账号（注册时选「我是租客」）后，在这里把他关联到
        <span class="font-semibold text-foreground">{{ tenant?.name || '该租户' }}</span>
        的档案上。<span class="text-foreground font-medium">关联即审核通过</span>，租客随后就能看到自己的登记信息、账单与合同。
      </div>

      <div v-if="loading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>

      <template v-else>
        <!-- 已关联：显示当前账号并提供解绑 -->
        <div v-if="account?.bound" class="rounded-xl border border-border p-3 space-y-2">
          <div class="flex items-center justify-between gap-3">
            <span class="text-muted-foreground text-xs">当前关联账号</span>
            <span class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">已关联</span>
          </div>
          <div class="text-sm font-semibold text-foreground truncate">{{ account.username }}</div>
          <div v-if="account.phone" class="text-[11px] text-muted-foreground">注册手机号 {{ account.phone }}</div>
          <p class="text-[11px] leading-5 text-muted-foreground pt-1">
            解除关联后该账号会回到「等待房东关联」状态（账号不删除），可再关联到别的租户。
          </p>
        </div>

        <!-- 未关联：搜索候选账号 -->
        <template v-else>
          <div>
            <label class="block text-xs font-semibold text-muted-foreground mb-1.5">搜索租客账号（用户名至少 2 个字）</label>
            <div class="flex items-center gap-2">
              <input
                v-model="keyword"
                class="input-field flex-1"
                placeholder="输入租客注册的用户名"
                @keyup.enter="search"
              />
              <button class="btn-ghost !py-2 shrink-0" :disabled="searching" @click="search">
                {{ searching ? '搜索中…' : '搜索' }}
              </button>
            </div>
          </div>

          <div v-if="searched && candidates.length === 0" class="rounded-xl border border-border px-3 py-4 text-center text-xs text-muted-foreground">
            没有找到可关联的租客账号。请确认租客已经在注册页选「我是租客」注册过，
            且还没有被关联到别的租户。
          </div>

          <div v-if="candidates.length" class="space-y-1.5">
            <button
              v-for="c in candidates"
              :key="c.id"
              type="button"
              class="w-full text-left rounded-xl border px-3 py-2.5 transition-colors"
              :class="selectedId === c.id
                ? 'border-brand-500/60 bg-brand-500/[0.08]'
                : 'border-border bg-surface/80 hover:bg-muted/60'"
              @click="selectedId = c.id"
            >
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs font-semibold text-foreground truncate">{{ c.username }}</span>
                <span v-if="selectedId === c.id" class="text-[11px] font-semibold text-brand-600 dark:text-brand-300 shrink-0">已选中</span>
              </div>
              <div class="mt-0.5 text-[11px] text-muted-foreground">让租户把用户名发给你，核对无误再关联</div>
            </button>
          </div>
        </template>
      </template>
    </div>

    <div class="flex items-center gap-3 pt-4">
      <button type="button" class="btn-ghost flex-1" :disabled="busy" @click="emit('update:modelValue', false)">关闭</button>
      <button
        v-if="account?.bound"
        type="button"
        class="flex-1 rounded-xl px-4 py-2.5 text-sm font-semibold text-white bg-rose-500 hover:bg-rose-600 transition-colors disabled:opacity-60"
        :disabled="busy"
        @click="askUnbind"
      >
        {{ busy ? '处理中…' : '解除关联' }}
      </button>
      <button
        v-else
        type="button"
        class="btn-brand flex-1"
        :disabled="busy || !selectedId"
        @click="bind"
      >
        {{ busy ? '关联中…' : '关联并开通' }}
      </button>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import Modal from './Modal.vue'
import {
  getTenantAccount,
  searchTenantAccountCandidates,
  bindTenantAccount,
  unbindTenantAccount,
  type TenantAccount,
  type TenantAccountCandidate,
} from '../api/tenant'
import type { Tenant } from '../api/rental'
import { toast } from '../utils/toast'
import { serverErrorMessage } from '../utils/apiError'

const props = defineProps<{
  modelValue: boolean
  tenant: Tenant | null
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 关联/解绑成功后通知父组件刷新租户列表（关联状态会显示在列表上）。 */
  changed: []
  /** 请求解绑确认：由父组件用 ConfirmDialog 弹（本组件不内嵌确认，避免叠两层弹窗）。 */
  requestUnbind: []
}>()

const loading = ref(false)
const busy = ref(false)
const searching = ref(false)
const searched = ref(false)
const account = ref<TenantAccount | null>(null)
const candidates = ref<TenantAccountCandidate[]>([])
const keyword = ref('')
const selectedId = ref<number | null>(null)

watch(() => props.modelValue, (open) => {
  if (!open) return
  account.value = null
  candidates.value = []
  keyword.value = ''
  selectedId.value = null
  searched.value = false
  void loadAccount()
})

async function loadAccount() {
  if (!props.tenant) return
  loading.value = true
  try {
    const res = await getTenantAccount(props.tenant.id)
    if (res.data?.code === 0) {
      account.value = res.data.data
    } else {
      toast(res.data?.message || '加载关联状态失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '加载关联状态失败'), 'error')
  } finally {
    loading.value = false
  }
}

async function search() {
  const q = keyword.value.trim()
  if ([...q].length < 2) {
    toast('请至少输入 2 个字', 'error')
    return
  }
  searching.value = true
  try {
    const res = await searchTenantAccountCandidates(q)
    if (res.data?.code === 0) {
      candidates.value = Array.isArray(res.data.data) ? res.data.data : []
    } else {
      toast(res.data?.message || '搜索失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '搜索失败'), 'error')
  } finally {
    searched.value = true
    searching.value = false
  }
}

async function bind() {
  if (!props.tenant || !selectedId.value) return
  busy.value = true
  try {
    const res = await bindTenantAccount(props.tenant.id, selectedId.value)
    if (res.data?.code === 0) {
      toast('已关联，租客现在可以查看自己的资料与账单了')
      emit('changed')
      emit('update:modelValue', false)
    } else {
      toast(res.data?.message || '关联失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '关联失败'), 'error')
  } finally {
    busy.value = false
  }
}

// 解绑交给父组件确认（ConfirmDialog），这里只把请求转上去；确认后父组件
// 调 doUnbind()。
function askUnbind() {
  emit('requestUnbind')
}

async function doUnbind() {
  if (!props.tenant) return
  busy.value = true
  try {
    const res = await unbindTenantAccount(props.tenant.id)
    if (res.data?.code === 0) {
      toast('已解除关联')
      emit('changed')
      emit('update:modelValue', false)
    } else {
      toast(res.data?.message || '解除失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '解除失败'), 'error')
  } finally {
    busy.value = false
  }
}

defineExpose({ doUnbind })
</script>
