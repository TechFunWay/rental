<template>
  <!--
    台账切换器（顶栏）：个人台账 + 我参与的共同台账 + 待接受的邀请。
    0.5.2 起业务数据按台账隔离，所以顶部必须随时能看出「我现在在哪个台账」，
    并且能一键切走 / 新建共同台账 / 管成员。
  -->
  <div class="relative" ref="rootRef" :class="props.fluid ? 'w-full' : ''">
    <button
      type="button"
      class="flex items-center gap-2 h-9 sm:h-10 pl-3 pr-2.5 rounded-xl bg-muted/70 border border-border hover:bg-muted transition-colors"
      :class="props.fluid ? 'w-full' : 'max-w-[11rem] sm:max-w-[15rem]'"
      :aria-expanded="open"
      aria-haspopup="listbox"
      :title="`切换台账（当前：${currentName}）`"
      @click.stop="toggle"
    >
      <span class="w-2 h-2 rounded-full shrink-0" :class="dotClass(ledgerStore.current)"></span>
      <span class="text-sm font-semibold text-foreground truncate flex-1 text-left">{{ currentName }}</span>
      <span
        v-if="ledgerStore.pendingInvites.length"
        class="shrink-0 min-w-[18px] h-[18px] px-1 rounded-full text-[11px] font-bold bg-amber-500 text-white flex items-center justify-center tabular-nums"
        title="待接受的邀请"
      >{{ ledgerStore.pendingInvites.length }}</span>
      <svg class="w-4 h-4 shrink-0 text-muted-foreground transition-transform" :class="open ? 'rotate-180' : ''" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
      </svg>
    </button>

    <transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0 scale-95 -translate-y-1"
      leave-active-class="transition duration-100"
      leave-to-class="opacity-0 scale-95 -translate-y-1"
    >
      <!--
        窄屏（< sm）不能再用「右对齐的 320px 下拉」：切换器本身离右边缘就很近，
        320px 会横向溢出被裁掉（2026-10-10 手机端实测左边溢出 82px，台账名看不见）。
        手机端改成贴住屏幕两侧的整宽面板，挂在顶栏下方；sm 起恢复右侧下拉。
        顶栏在窄屏是两行（品牌行 + 切换器行），压缩后约 96px，面板 top 取 6.5rem（104px）
        贴在其下方，否则面板会盖住第二行的切换器本体。
        高度上限按「视口 − 顶栏 − 底部导航」给，保证最后一项与新建入口都能滚到、不被压住。
      -->
      <div
        v-if="open"
        ref="panelRef"
        class="fixed inset-x-3 top-[6.5rem] sm:absolute sm:inset-x-auto sm:right-0 sm:top-full sm:mt-2 w-auto sm:w-80 rounded-2xl surface shadow-card p-1.5 sm:origin-top-right z-50 max-h-[calc(100dvh-12rem)] sm:max-h-[70dvh] overflow-y-auto"
        role="listbox"
      >
        <div class="px-3 py-2 border-b border-border mb-1">
          <div class="text-xs font-semibold text-foreground">切换台账</div>
          <div class="text-[11px] text-muted-foreground leading-snug mt-0.5">
            数据按台账隔离，切换后各页面只看该台账的数据。
          </div>
        </div>

        <!-- 个人台账 -->
        <button
          v-for="l in personalLedgers"
          :key="l.id"
          type="button"
          role="option"
          :aria-selected="ledgerStore.selection === String(l.id)"
          class="w-full flex items-center gap-2.5 px-3 py-2.5 rounded-xl text-left transition-colors"
          :class="ledgerStore.selection === String(l.id) ? 'bg-brand-500/10' : 'hover:bg-muted'"
          @click="pick(l)"
        >
          <span class="w-2 h-2 rounded-full shrink-0 bg-brand-500"></span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-semibold text-foreground truncate">我的台账</span>
            <span class="block text-[11px] text-muted-foreground truncate">只有我自己能用 · 个人数据默认隔离</span>
          </span>
          <svg v-if="ledgerStore.selection === String(l.id)" class="w-4 h-4 shrink-0 text-brand-600 dark:text-brand-300" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
        </button>

        <!-- 共同台账 -->
        <div v-if="sharedLedgers.length" class="px-3 pt-3 pb-1 text-[11px] font-bold text-muted-foreground uppercase tracking-widest">共同台账</div>
        <div
          v-for="l in sharedLedgers"
          :key="l.id"
          class="flex items-center gap-2 rounded-xl transition-colors"
          :class="ledgerStore.selection === String(l.id) ? 'bg-brand-500/10' : 'hover:bg-muted'"
        >
          <button type="button" role="option" class="min-w-0 flex-1 flex items-center gap-2.5 px-3 py-2.5 text-left" :aria-selected="ledgerStore.selection === String(l.id)" @click="pick(l)">
            <span class="w-2 h-2 rounded-full shrink-0 bg-sky-500"></span>
            <span class="min-w-0 flex-1">
              <span class="flex items-center gap-1.5">
                <span class="text-sm font-semibold text-foreground truncate">{{ l.name }}</span>
                <span v-if="l.role === 'owner'" class="badge !px-1.5 !py-0 text-[10px] bg-brand-500/15 text-brand-600 dark:text-brand-300 shrink-0">我创建的</span>
              </span>
              <span class="block text-[11px] text-muted-foreground truncate">
                {{ l.role === 'owner' ? '管理者' : '成员' }} · {{ l.member_count }} 人
              </span>
            </span>
            <svg v-if="ledgerStore.selection === String(l.id)" class="w-4 h-4 shrink-0 text-brand-600 dark:text-brand-300" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
          </button>
          <div class="flex items-center gap-0.5 pr-2 shrink-0">
            <!-- 共同台账都能打开「成员与权限」：管理者能邀请/勾权限/移除，
                 普通成员看只读列表并可以主动退出（个人台账没有成员可管，不显示）。 -->
            <button
              v-if="l.kind === 'shared'"
              type="button"
              class="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted"
              title="成员与权限"
              @click.stop="openMembers(l)"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-1.13a4 4 0 10-4-4 4 4 0 004 4zm6-4a3 3 0 11-3-3"/></svg>
            </button>
            <button
              type="button"
              class="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted"
              title="台账设置"
              @click.stop="openSettings(l)"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
            </button>
          </div>
        </div>

        <!-- 待接受的邀请 -->
        <template v-if="ledgerStore.pendingInvites.length">
          <div class="px-3 pt-3 pb-1 text-[11px] font-bold text-amber-600 dark:text-amber-300 uppercase tracking-widest">待接受的邀请</div>
          <div v-for="l in ledgerStore.pendingInvites" :key="l.id" class="px-3 py-2.5 rounded-xl bg-amber-400/10 mb-1">
            <div class="text-sm text-foreground leading-snug">
              <span class="font-semibold">{{ l.owner_name || '对方' }}</span> 邀请你加入
              <span class="font-semibold">「{{ l.name }}」</span>
            </div>
            <div class="flex items-center gap-2 mt-2">
              <button type="button" class="flex-1 px-3 py-1.5 rounded-lg text-xs font-semibold bg-brand-500 text-white hover:bg-brand-600 transition-colors" :disabled="busy === l.id" @click.stop="respond(l, true)">接受</button>
              <button type="button" class="flex-1 px-3 py-1.5 rounded-lg text-xs font-semibold text-muted-foreground hover:bg-muted transition-colors" :disabled="busy === l.id" @click.stop="respond(l, false)">拒绝</button>
            </div>
          </div>
        </template>

        <!-- 新建共同台账 -->
        <div class="mt-1 pt-1 border-t border-border">
          <button
            type="button"
            class="w-full flex items-center gap-2.5 px-3 py-2.5 rounded-xl text-left hover:bg-muted transition-colors"
            @click.stop="openCreate"
          >
            <span class="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground shrink-0">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/></svg>
            </span>
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-semibold text-foreground">新建共同台账</span>
              <span class="block text-[11px] text-muted-foreground">邀请家人或同事一起管房源与账单</span>
            </span>
          </button>
        </div>
      </div>
    </transition>

    <LedgerCreateModal v-model="createOpen" @created="onCreated" />
    <LedgerMembersModal v-model="membersOpen" :ledger="membersTarget" @changed="reload" />
    <LedgerSettingsModal v-model="settingsOpen" :ledger="settingsTarget" @changed="reload" />
  </div>
</template>

<script setup lang="ts">
// 台账切换器：只负责改 store 里的选择 / 打开各个弹窗。
// 切换后页面整体重挂载由 MainLayout 的 RouterView :key 负责，这里不做页面刷新。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useLedgerStore } from '../stores/ledger'
import { respondInvitation, type LedgerView } from '../api/ledger'
import { toast } from '../utils/toast'
import LedgerCreateModal from './LedgerCreateModal.vue'
import LedgerMembersModal from './LedgerMembersModal.vue'
import LedgerSettingsModal from './LedgerSettingsModal.vue'

const ledgerStore = useLedgerStore()

// fluid：窄屏顶栏第二行用，按钮撑满整行（默认是顶栏右组的自适应胶囊）。
const props = withDefaults(defineProps<{ fluid?: boolean }>(), { fluid: false })
const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const busy = ref<number | null>(null)

const createOpen = ref(false)
const membersOpen = ref(false)
const settingsOpen = ref(false)
const membersTarget = ref<LedgerView | null>(null)
const settingsTarget = ref<LedgerView | null>(null)

const personalLedgers = computed(() => ledgerStore.activeLedgers.filter((l) => l.kind === 'personal'))
const sharedLedgers = computed(() => ledgerStore.activeLedgers.filter((l) => l.kind === 'shared'))
const currentName = computed(() => {
  const c = ledgerStore.current
  if (!c) return '选择台账'
  return c.kind === 'personal' ? '我的台账' : c.name
})

function dotClass(l: LedgerView | null): string {
  if (!l) return 'bg-muted-foreground/40'
  return l.kind === 'personal' ? 'bg-brand-500' : 'bg-sky-500'
}

function toggle() {
  open.value = !open.value
}

function pick(l: LedgerView) {
  open.value = false
  ledgerStore.switchLedger(l.id)
}

function openCreate() {
  open.value = false
  createOpen.value = true
}

function openMembers(l: LedgerView) {
  open.value = false
  membersTarget.value = l
  membersOpen.value = true
}

function openSettings(l: LedgerView) {
  open.value = false
  settingsTarget.value = l
  settingsOpen.value = true
}

async function respond(l: LedgerView, accept: boolean) {
  busy.value = l.id
  try {
    const res = await respondInvitation(l.id, accept)
    if (res.data?.code === 0) {
      toast(accept ? '已加入共同台账' : '已拒绝邀请')
      await reload()
      if (accept) ledgerStore.switchLedger(l.id)
    } else {
      toast(res.data?.message || '处理邀请失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '处理邀请失败', 'error')
  } finally {
    busy.value = null
  }
}

async function reload() {
  await ledgerStore.load(true)
}

function onCreated(ledgerId: number) {
  void reload().then(() => {
    ledgerStore.switchLedger(ledgerId)
    toast('共同台账已创建')
  })
}

function onClickOutside(e: MouseEvent) {
  if (!open.value) return
  const target = e.target as Node
  if (rootRef.value?.contains(target) || panelRef.value?.contains(target)) return
  open.value = false
}

onMounted(() => document.addEventListener('click', onClickOutside))
onBeforeUnmount(() => document.removeEventListener('click', onClickOutside))
</script>
