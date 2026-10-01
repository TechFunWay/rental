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
                @click="support.confirmSupported()"
              >
                <svg v-if="!support.sending" class="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
                {{ support.sending ? '发送中…' : '已支持' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { useSupportStore } from '../stores/support'
import donateQr from '../assets/donate-wechat.png'

const support = useSupportStore()
</script>
