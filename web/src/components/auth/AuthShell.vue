<template>
  <div class="auth-root">
    <!-- Immersive background layers -->
    <div class="bg-base"></div>
    <div class="aurora aurora-1"></div>
    <div class="aurora aurora-2"></div>
    <div class="aurora aurora-3"></div>
    <div class="grid-overlay"></div>
    <div class="grain"></div>
    <div class="vignette"></div>

    <!-- Content -->
    <!-- 手机端（<640px）整页收紧：页边距 24→16、上下留白 48→20，品牌区与卡片
         内边距同步缩小，目标是登录/注册一屏内不出滚动条（375×667）。 -->
    <div class="auth-content relative z-10 flex flex-col items-center justify-center px-4 py-5 sm:px-6 sm:py-12">
      <div class="w-full max-w-md">
        <!-- Brand -->
        <div class="flex flex-col items-center text-center mb-3.5 sm:mb-7 animate-fade-in">
          <div class="logo-badge">
            <span class="font-display font-extrabold text-lg sm:text-xl text-white">{{ siteInitial }}</span>
          </div>
          <h1 class="mt-2 sm:mt-4 font-display text-base sm:text-lg font-bold tracking-tight text-white/90">{{ authStore.siteTitle }}</h1>
        </div>

        <!-- Glass card -->
        <div class="card-wrap animate-scale-in">
          <div class="card-glow"></div>
          <div class="auth-card">
            <div class="relative z-10">
              <h2 class="font-display text-[1.35rem] sm:text-[1.75rem] leading-tight font-extrabold tracking-tight text-white">{{ title }}</h2>
              <p v-if="subtitle" class="mt-1 sm:mt-2 text-xs sm:text-sm leading-5 text-white/50">{{ subtitle }}</p>
              <div class="mt-4 sm:mt-7">
                <slot />
              </div>
            </div>
          </div>
        </div>

        <!-- Footer -->
        <div v-if="$slots.footer" class="mt-4 sm:mt-7 text-center text-xs sm:text-sm text-white/50">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '../../stores/auth'

defineProps<{ title: string; subtitle?: string }>()

const authStore = useAuthStore()
const siteInitial = computed(() => (authStore.siteTitle || 'S').charAt(0).toUpperCase())
</script>

<style scoped>
@property --bd-angle {
  syntax: '<angle>';
  initial-value: 0deg;
  inherits: false;
}

.auth-root {
  position: relative;
  min-height: 100vh;
  min-height: 100dvh; /* 手机端浏览器工具栏收展时 100vh 会超出可视区，dvh 跟随可视高度 */
  overflow: hidden;
  background: #05050b;
  color: #fff;
}

/* content：与 auth-root 同高，dvh 跟随手机端可视高度（键盘弹出/工具栏收展不抖） */
.auth-content {
  min-height: 100vh;
  min-height: 100dvh;
}

/* base radial wash */
.bg-base {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(125% 125% at 50% -10%, #14142a 0%, #0a0a16 45%, #05050b 100%);
}

/* drifting aurora orbs。背景层一律 absolute（auth-root 是 relative 满屏容器），
   不用 fixed：手机 WebView 里 fixed 层在键盘弹出等视口变化时会重锚定，
   表现为局部闪烁；absolute 随文档定位，不参与视口重排。
   模糊滤镜只在桌面端保留（大半径 blur 在弱 WebView 里栅格化慢，局部画面
   迟迟出不来）；径向渐变本身是软边，去掉滤镜观感几乎无差。 */
.aurora {
  position: absolute;
  border-radius: 9999px;
  will-change: transform;
}
@media (min-width: 641px) {
  .aurora { filter: blur(90px); }
}
.aurora-1 {
  width: 42rem;
  height: 42rem;
  top: -12rem;
  left: -8rem;
  background: radial-gradient(circle at 30% 30%, rgba(99, 102, 241, 0.55), transparent 70%);
  animation: float-1 20s ease-in-out infinite;
}
.aurora-2 {
  width: 38rem;
  height: 38rem;
  bottom: -14rem;
  right: -10rem;
  background: radial-gradient(circle at 70% 70%, rgba(168, 85, 247, 0.5), transparent 70%);
  animation: float-2 24s ease-in-out infinite;
}
.aurora-3 {
  width: 30rem;
  height: 30rem;
  top: 40%;
  left: 45%;
  background: radial-gradient(circle at 50% 50%, rgba(34, 211, 238, 0.35), transparent 70%);
  animation: float-3 28s ease-in-out infinite;
}
@keyframes float-1 {
  0%, 100% { transform: translate(0, 0) scale(1); }
  50% { transform: translate(5rem, 4rem) scale(1.12); }
}
@keyframes float-2 {
  0%, 100% { transform: translate(0, 0) scale(1); }
  50% { transform: translate(-6rem, -3rem) scale(1.06); }
}
@keyframes float-3 {
  0%, 100% { transform: translate(-50%, -50%) scale(1); }
  50% { transform: translate(-58%, -42%) scale(1.18); }
}

/* faint grid that fades toward the edges */
.grid-overlay {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.035) 1px, transparent 1px);
  background-size: 58px 58px;
  -webkit-mask: radial-gradient(circle at 50% 42%, #000 0%, transparent 72%);
  mask: radial-gradient(circle at 50% 42%, #000 0%, transparent 72%);
}

/* fine film grain */
.grain {
  position: absolute;
  inset: 0;
  opacity: 0.16;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}

/* edge darkening */
.vignette {
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: radial-gradient(125% 110% at 50% 50%, transparent 48%, rgba(0, 0, 0, 0.55) 100%);
}

/* card + glow */
.card-wrap {
  position: relative;
}
.card-glow {
  position: absolute;
  inset: -28px;
  z-index: 0;
  border-radius: 40px;
  background: radial-gradient(60% 55% at 50% 0%, rgba(124, 92, 255, 0.45), transparent 72%);
}
@media (min-width: 641px) {
  /* 卡片辉光的模糊同样只在桌面端做（一次性栅格化，手机端省掉首帧延迟） */
  .card-glow { filter: blur(46px); }
}
.auth-card {
  position: relative;
  z-index: 1;
  /* 手机端内边距 36px → 20px（卡片高度占屏比明显下降），桌面端在下方媒体查询恢复 */
  border-radius: 20px;
  padding: 1.25rem;
  background: rgba(18, 18, 30, 0.55);
  backdrop-filter: blur(26px) saturate(140%);
  -webkit-backdrop-filter: blur(26px) saturate(140%);
  border: 1px solid rgba(255, 255, 255, 0.07);
  box-shadow:
    0 30px 80px -24px rgba(0, 0, 0, 0.75),
    inset 0 1px 0 rgba(255, 255, 255, 0.06);
  overflow: hidden;
}
/* rotating conic flow border */
.auth-card::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  padding: 1px;
  background: conic-gradient(
    from var(--bd-angle),
    transparent 0%,
    rgba(99, 102, 241, 0) 18%,
    #6366f1 32%,
    #a855f7 44%,
    #22d3ee 56%,
    rgba(34, 211, 238, 0) 70%,
    transparent 100%
  );
  -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
  mask-composite: exclude;
  animation: border-rotate 7s linear infinite;
  pointer-events: none;
}
@keyframes border-rotate {
  to { --bd-angle: 360deg; }
}

/* logo */
.logo-badge {
  /* 手机端 56px → 44px，桌面端在媒体查询里恢复，避免品牌区吃掉首屏高度 */
  width: 2.75rem;
  height: 2.75rem;
  border-radius: 0.9rem;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 50%, #a855f7 100%);
  box-shadow:
    0 0 0 1px rgba(255, 255, 255, 0.12),
    0 12px 36px -8px rgba(124, 92, 255, 0.7);
}

@media (prefers-reduced-motion: reduce) {
  .aurora { animation: none; }
  .auth-card::before { animation: none; }
}

/* 手机端降噪（v0.4.0）：登录页是手机上的第一屏，此前三团极光带 blur(90px)
   无限漂浮、卡片实时毛玻璃叠在会动的背景上（每帧重算）、流光边框动画的是
   CSS 自定义属性（无法 GPU 合成，每帧重绘），三者叠加让页面永远满负荷渲染，
   手机上明显卡顿。640px 以下停掉无限动画与 backdrop-filter，用静态渐变保住
   观感；桌面端维持完整效果。飞牛手机 App 的内嵌 WebView 更弱：fixed 大图层
   在键盘弹出时重锚定会局部闪烁、大半径模糊栅格化慢会局部迟迟不出画面，
   因此背景层 absolute（见上）、模糊滤镜全部只留桌面端，网格与噪点纹理层
   在手机端直接不渲染。 */
@media (max-width: 640px) {
  .aurora {
    animation: none;
    will-change: auto;
  }
  .auth-card::before {
    animation: none;
  }
  .auth-card {
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
    /* 毛玻璃撤掉后把底色加实，保持卡片与背景的层次 */
    background: rgba(15, 15, 26, 0.92);
  }
  .grid-overlay,
  .grain {
    display: none;
  }
}

/* 桌面端恢复：手机端收紧的卡片内边距/圆角与品牌徽标只作用于 <640px。
   这条媒体查询必须放在 .auth-card / .logo-badge 定义**之后**——同优先级下
   后写的规则生效，放前面会被基础规则盖掉（首版就这么错过一次）。 */
@media (min-width: 641px) {
  .auth-card {
    border-radius: 26px;
    padding: 2.25rem;
  }
  .logo-badge {
    width: 3.5rem;
    height: 3.5rem;
    border-radius: 1.1rem;
  }
}
</style>
