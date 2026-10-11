<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0"
      enter-to-class="opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div v-if="support.show" class="fixed inset-0 z-[70] flex items-center justify-center p-4" @click.self="support.dismiss()">
        <div class="absolute inset-0 bg-black/50 backdrop-blur-sm"></div>
        <div class="relative surface rounded-2xl shadow-card w-full max-w-[320px] text-center overflow-hidden">
          <button
            class="absolute top-2.5 right-2.5 z-10 p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
            aria-label="关闭"
            @click="support.dismiss()"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
          </button>

          <div class="px-5 pt-5 pb-4">
            <div class="w-11 h-11 mx-auto mb-2 rounded-xl bg-brand-gradient flex items-center justify-center shadow-glow">
              <span class="text-xl">☕</span>
            </div>
            <h3 class="text-base font-bold text-foreground mb-1.5">请作者喝杯咖啡</h3>

            <p class="text-xs text-muted-foreground leading-relaxed mb-3">
              应用免费无广告、数据保存在本机。若对你有帮助，欢迎赞赏——<strong class="text-foreground">金额随意，1 元也是心意</strong>。
            </p>

            <div class="flex justify-center my-2">
              <div>
                <img
                  :src="donateQr"
                  alt="微信赞赏码"
                  class="w-[150px] h-auto rounded-lg border border-border bg-white p-1 shadow-card"
                />
                <span class="block mt-1 text-[11px] text-muted-foreground">微信扫码赞赏</span>
              </div>
            </div>

            <p class="text-[11px] text-muted-foreground/80 leading-4 mt-2">
              已支付可点「已支持」发送一次匿名计数（与支付记录无关）
            </p>
            <p v-if="support.errorText" class="text-xs text-destructive mt-1.5">{{ support.errorText }}</p>

            <div class="flex justify-center gap-2.5 mt-3.5">
              <button
                class="flex-1 px-3 py-2 rounded-lg text-sm font-semibold text-muted-foreground hover:bg-muted transition-colors"
                :disabled="support.sending"
                @click="support.dismiss()"
              >
                暂不支持
              </button>
              <button
                class="flex-1 px-3 py-2 rounded-lg text-sm font-semibold bg-brand-gradient text-white shadow-glow hover:opacity-90 transition-opacity disabled:opacity-50 flex items-center justify-center gap-1.5"
                :disabled="support.sending"
                @click="openAmount"
              >
                <svg v-if="!support.sending" class="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
                {{ support.sending ? '发送中…' : '已支持' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 金额输入弹窗（点击「已支持」后出现）。
         独立于主弹窗、直接挂到 body：卡片上的 backdrop-blur 会让内部
         fixed 元素相对卡片定位，嵌在卡片里会导致弹窗错位。 -->
    <div v-if="amountDialog" class="fixed inset-0 z-[80] overflow-y-auto overscroll-contain bg-black/50 backdrop-blur-sm" @click="closeAmount">
      <div class="flex min-h-full items-center justify-center p-4">
        <div class="relative surface rounded-2xl shadow-card w-full max-w-[300px] p-4 text-center" @click.stop>
          <h4 class="text-sm font-bold text-foreground">填写赞赏金额</h4>
          <p class="mt-1 text-[11px] text-muted-foreground">仅用于统计，不做支付核验</p>
          <div class="mt-2.5 flex flex-wrap justify-center gap-1.5">
            <button
              v-for="p in presets" :key="p"
              class="rounded-full border border-border bg-muted/40 px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
              :class="{ '!border-brand-500 !bg-brand-500/10 !text-brand-600 dark:!text-brand-300': amount === p }"
              @click="selectPreset(p)"
            >
              {{ p }} 元
            </button>
          </div>
          <div class="mt-2.5 flex items-center justify-center gap-1.5">
            <span class="text-xs text-muted-foreground">或输入</span>
            <input
              v-model.number="customAmount"
              type="number"
              min="0"
              step="0.01"
              placeholder="金额（元）"
              class="input-field w-24 text-center text-sm"
              @input="amount = Number(customAmount) || 0"
            />
          </div>
          <p v-if="amount === 0" class="mt-1.5 text-[11px] text-amber-500">未填写将以 0 元上报</p>
          <p v-if="support.errorText" class="mt-1.5 text-xs text-destructive">{{ support.errorText }}</p>
          <div class="mt-3 flex gap-2">
            <button class="btn-ghost flex-1 text-sm py-1.5" :disabled="support.sending" @click="closeAmount">返回</button>
            <button class="btn-brand flex-1 text-sm py-1.5" :disabled="support.sending" @click="confirmAmount">
              {{ support.sending ? '发送中…' : '确定' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useSupportStore } from '../stores/support'
import donateQr from '../assets/donate-wechat.png'

const support = useSupportStore()

// 金额输入弹窗状态
const amountDialog = ref(false)
const presets = [1, 5, 10, 50]
const customAmount = ref<number | ''>('')
const amount = ref<number>(0)

// 主弹窗关闭时（含关闭按钮/遮罩/发送成功）一并复位金额弹窗
watch(
  () => support.show,
  (visible) => {
    if (!visible) resetAmount()
  },
)

function resetAmount() {
  amountDialog.value = false
  customAmount.value = ''
  amount.value = 0
}

function openAmount() {
  resetAmount()
  support.errorText = ''
  amountDialog.value = true
}

function selectPreset(p: number) {
  amount.value = p
  customAmount.value = ''
}

function closeAmount() {
  if (support.sending) return
  resetAmount()
}

function confirmAmount() {
  if (support.sending) return
  void support.confirmSupported(amount.value || 0)
}
</script>
