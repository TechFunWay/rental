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
        <div class="relative surface rounded-2xl shadow-card w-full max-w-sm text-center overflow-hidden">
          <button
            class="absolute top-3 right-3 z-10 p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
            aria-label="关闭"
            @click="support.dismiss()"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
          </button>

          <div class="px-6 pt-7 pb-6">
            <div class="w-14 h-14 mx-auto mb-3 rounded-2xl bg-brand-gradient flex items-center justify-center shadow-glow">
              <span class="text-2xl">☕</span>
            </div>
            <h3 class="text-lg font-bold text-foreground mb-2">请作者喝杯咖啡</h3>

            <p class="text-sm text-muted-foreground leading-relaxed mb-1">
              这个应用免费、无广告，数据完全保存在你自己的设备上。
            </p>
            <p class="text-sm text-muted-foreground leading-relaxed mb-4">
              如果它帮到了你，欢迎请作者喝杯咖啡——<strong class="text-foreground">金额随意，1 元也是心意</strong>。
              <br />
              <span class="text-xs">不赞赏也完全没有问题，<strong>不支付不影响任何功能</strong>。</span>
            </p>

            <div class="flex justify-center my-4">
              <div>
                <img
                  :src="donateQr"
                  alt="微信赞赏码"
                  class="w-[190px] h-auto rounded-xl border border-border bg-white p-1 shadow-card"
                />
                <span class="block mt-2 text-xs text-muted-foreground">微信扫码赞赏</span>
              </div>
            </div>

            <p class="text-xs text-muted-foreground leading-5 mt-3">
              支付后可点击下方按钮发送一次匿名支持计数<br />
              （仅设备统计信息，与你的微信账号和支付记录无任何关联）
            </p>
            <p v-if="support.errorText" class="text-xs text-destructive mt-2">{{ support.errorText }}</p>

            <div class="flex justify-center gap-3 mt-5">
              <button
                class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold text-muted-foreground hover:bg-muted transition-colors"
                :disabled="support.sending"
                @click="support.dismiss()"
              >
                暂不支持
              </button>
              <button
                class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold bg-brand-gradient text-white shadow-glow hover:opacity-90 transition-opacity disabled:opacity-50 flex items-center justify-center gap-1.5"
                :disabled="support.sending"
                @click="support.confirmSupported()"
              >
                <svg v-if="!support.sending" class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
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
