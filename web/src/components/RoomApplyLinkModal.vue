<template>
  <Modal :model-value="modelValue" title="房源登记链接" @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3">
      <div class="rounded-xl border border-brand-500/30 bg-brand-500/[0.06] px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        把这个链接（或二维码）发到村民群 / 贴在村委会：对方<span class="font-medium text-foreground">不用注册登录</span>，
        扫码就能登记要出租的房屋（可顺手填上现有租户）。提交后进入「待审房源」，
        你核对通过才会建入台账——空房都在台账里，有人来问就能查、能推荐。
      </div>

      <div v-if="loading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
      <template v-else>
        <div>
          <span class="text-xs font-semibold text-muted-foreground block mb-1.5">登记链接</span>
          <textarea
            :value="link"
            readonly
            rows="2"
            class="input-field !py-2 text-xs break-all resize-none"
            @focus="($event.target as HTMLTextAreaElement).select()"
          ></textarea>
        </div>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn-brand !py-2 !px-3 text-xs" @click="copyLink">
            {{ copied ? '已复制 ✓' : '复制链接' }}
          </button>
          <button type="button" class="btn-ghost !py-2 !px-3 text-xs" :disabled="regenerating" @click="regenerate">
            {{ regenerating ? '生成中…' : '重新生成（旧链接失效）' }}
          </button>
        </div>

        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
          <div>归属台账：<span class="text-foreground font-medium">{{ ledgerName || '当前台账' }}</span></div>
          <div>待审房源：<span class="text-foreground font-medium tabular-nums">{{ pendingCount }}</span> 条</div>
        </div>
        <div class="rounded-xl border px-3 py-2.5 text-[11px] leading-5" :class="scopeNote.cls">
          {{ scopeNote.text }}
        </div>

        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="emit('update:modelValue', false)">关闭</button>
        </div>
      </template>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Modal from './Modal.vue'
import { useLedgerStore } from '../stores/ledger'
import { toast } from '../utils/toast'
import { resolveApplyLinkScope } from '../utils/net'
import { getLedgerRoomApplyLink } from '../api/rental'

// 台账级「房源登记链接」（村委/中介场景）：一个台账一个 token，免登录登记房源。
// 链接基址的判定与租客登记链接同一套（网关域要换成应用直连地址）。
const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const ledgerStore = useLedgerStore()
const loading = ref(false)
const regenerating = ref(false)
const error = ref('')
const copied = ref(false)
const link = ref('')
const token = ref('')
const base = ref('')
const viaGateway = ref(false)
const configured = ref(false)
const ledgerName = ref('')
const pendingCount = ref(0)

// 链接可用范围提示：与租客登记链接同一套判定（网关地址 / 内网地址 / 已配公网基址 / 自动推导）
const scopeNote = computed(() => {
  const scope = resolveApplyLinkScope(base.value, {
    configured: configured.value,
    viaGateway: viaGateway.value,
    currentUrl: location.href,
  })
  switch (scope) {
    case 'gateway':
      return {
        cls: 'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400',
        text: '基址填的像是你正在使用的飞牛网关地址：村民没有 NAS 账号，点开只会看到 invalid token。请改成你自己的公网通道地址（内网穿透 / 端口映射 / 域名+DDNS）。',
      }
    case 'lan':
      return {
        cls: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
        text: `这条链接用的是应用直连（内网）地址 ${base.value}，只有同一网络的人打得开；要给外网村民用，先开公网通道，再到「系统配置 → 租客登记与单据 → 租客登记链接基址」填那个地址。`,
      }
    case 'custom':
      return {
        cls: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
        text: '已使用你配置的公网基址，可直接分享；建议先用手机流量打开一次，确认真能从外网访问。',
      }
    default:
      return {
        cls: 'bg-muted/60 text-muted-foreground',
        text: `链接按当前访问地址生成（${base.value}）；村民若打不开，请到「系统配置 → 租客登记与单据 → 租客登记链接基址」填写他们能访问的公网地址。`,
      }
  }
})

async function load(regenerate = false) {
  if (regenerate) regenerating.value = true
  else loading.value = true
  error.value = ''
  try {
    const res = await getLedgerRoomApplyLink(regenerate)
    if (res.data?.code === 0) {
      const d = res.data.data
      token.value = d?.token ?? ''
      link.value = d?.link ?? ''
      base.value = d?.base ?? ''
      viaGateway.value = d?.via_gateway ?? false
      configured.value = d?.configured ?? false
      ledgerName.value = d?.ledger_name ?? ledgerStore.current?.name ?? ''
      pendingCount.value = d?.pending_count ?? 0
    } else {
      error.value = res.data?.message || '生成登记链接失败'
    }
  } catch (err: any) {
    error.value = err.response?.data?.message || '生成登记链接失败，请重试'
  } finally {
    loading.value = false
    regenerating.value = false
  }
}

async function regenerate() {
  await load(true)
  if (!error.value) toast('已重新生成，旧链接失效')
}

async function copyLink() {
  try {
    await navigator.clipboard.writeText(link.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    error.value = '复制失败，请长按选中文本手动复制'
  }
}

watch(() => props.modelValue, (open) => {
  if (!open) return
  copied.value = false
  void load(false)
}, { immediate: true })
</script>
