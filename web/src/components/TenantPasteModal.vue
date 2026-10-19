<template>
  <!-- 口径见仓库内 docs/13-租户资料粘贴识别设计.md：外网不通、登记链接打不开时，
       租客把资料微信发来，房东在这里粘贴解析成草稿，核对后批量入库。 -->
  <Modal v-model="show" title="粘贴识别租户资料" wide>
    <!-- 布局口径（mobile-ui 规范）：手机端一屏放完「输入 → 识别 → 核对 → 入库」，
         草稿区高度有界（内部滚动），入库按钮固定在下沿不溢出；桌面端放宽。 -->
    <div class="space-y-2">
      <p class="text-[11px] text-muted-foreground leading-relaxed">
        租客按「姓名：张三」一行一个字段（中英文冒号都行）写成文字，微信发您；在这里整段粘贴 →「识别」→ 核对补全 → 一键入库。<span class="text-foreground">多位租户写在同一条消息里也能一起识别。</span>
      </p>

      <div>
        <label class="text-[11px] font-semibold text-muted-foreground block mb-1">租户发来的资料</label>
        <textarea
          v-model="text"
          class="input-field !py-1 min-h-[48px] text-xs leading-relaxed"
          placeholder="一行一个字段，字段名后面用冒号&#10;姓名：张三&#10;手机号：18956565845&#10;…多位租户依次再写一遍"
          @input="error = ''"
        ></textarea>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button class="btn-brand !py-1.5 !px-3.5 text-xs" :disabled="parsing || !text.trim()" @click="doParse">
          {{ parsing ? '识别中…' : '识别' }}
        </button>
        <button v-if="drafts.length" class="btn-ghost !py-1.5 !px-3.5 text-xs" :disabled="parsing" @click="doParse">重新识别</button>
        <span class="text-[11px] text-muted-foreground">识别结果只在本页核对，不会自动入库</span>
      </div>

      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

      <!-- 读不了的行原样带回：房东一眼看出哪句写岔了，改完再识别一次 -->
      <div v-if="unmatched.length" class="rounded-lg border border-amber-500/40 bg-amber-500/5 px-2.5 py-2 space-y-0.5">
        <p class="text-[11px] font-semibold text-amber-600 dark:text-amber-300">有 {{ unmatched.length }} 行没认出来（不会入库）</p>
        <p v-for="(u, i) in unmatched" :key="i" class="text-[10px] text-muted-foreground break-all">{{ u }}</p>
      </div>

      <!-- 草稿区：高度有界、内部滚动，保证下方入库按钮尽量在第一屏内。
           手机端收紧到 26vh（两位租户两卡 + 说明刚好一屏，第三位起内部滚动）。 -->
      <div v-if="drafts.length" class="space-y-1.5">
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs font-semibold text-muted-foreground">识别出 {{ drafts.length }} 位租户，核对后入库</span>
          <span class="text-[11px] text-muted-foreground">房源需已在「房源管理」建档</span>
        </div>

        <div class="space-y-1.5 max-h-[16vh] sm:max-h-[42vh] overflow-auto pr-0.5">
          <div
            v-for="(d, i) in drafts"
            :key="i"
            class="rounded-lg border border-border bg-surface/80 px-2.5 py-2 space-y-1.5"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="text-[11px] font-semibold text-muted-foreground">第 {{ i + 1 }} 位</span>
              <button type="button" class="text-[11px] text-destructive shrink-0" @click="dropDraft(i)">去掉</button>
            </div>

            <!-- 紧凑两列：姓名/手机一行、证件一行、小区/房号一行、日期与押金一行 -->
            <div class="grid grid-cols-2 gap-1.5">
              <input v-model="d.name" class="input-field !py-1.5 text-xs" placeholder="姓名 *" />
              <input v-model="d.phone" class="input-field !py-1.5 text-xs tabular-nums" inputmode="tel" placeholder="手机号" />
              <input v-model="d.id_card" class="input-field !py-1.5 text-xs tabular-nums col-span-2" placeholder="身份证号（选填）" />
              <input v-model="d.community" class="input-field !py-1.5 text-xs" placeholder="小区（选填）" list="paste-communities" />
              <input v-model="d.room_no" class="input-field !py-1.5 text-xs" placeholder="房号 *" />
              <div class="col-span-2 grid grid-cols-3 gap-1.5">
                <DateField v-model="d.move_in_date" type="date" placeholder="入住" />
                <DateField v-model="d.lease_end_date" type="date" placeholder="到期" />
                <input v-model.number="d.deposit" type="number" step="0.01" min="0" class="input-field !py-1.5 text-xs" placeholder="押金" />
              </div>
              <input v-model="d.notes" class="input-field !py-1.5 text-xs col-span-2" placeholder="备注（选填）" />
            </div>

            <p v-if="d.warnings.length" class="text-[10px] text-amber-600 dark:text-amber-300 leading-relaxed">
              {{ d.warnings.join('；') }}
            </p>
          </div>
        </div>

        <datalist id="paste-communities">
          <option v-for="c in communities" :key="c" :value="c" />
        </datalist>

        <div v-if="importResult" class="rounded-lg border border-border p-2.5 text-xs space-y-1">
          <div class="flex gap-3">
            <span class="text-emerald-600 dark:text-emerald-400">新建 {{ importResult.created }}</span>
            <span class="text-brand-600 dark:text-brand-300">更新 {{ importResult.updated }}</span>
            <span class="text-destructive">失败 {{ importResult.failed }}</span>
          </div>
          <ul v-if="importResult.errors.length" class="text-[11px] text-destructive space-y-0.5 max-h-24 overflow-auto">
            <li v-for="(e, i) in importResult.errors" :key="i">第 {{ e.line }} 位：{{ e.reason }}</li>
          </ul>
        </div>
        <p v-if="importError" class="text-sm text-destructive">{{ importError }}</p>

        <!-- 按钮区贴在面板底部：面板内容比视口高时，滚到底就能看到，
             不会出现「按钮在很下面、要点好几次才滚到」-->
        <div class="sticky bottom-0 -mx-4 px-4 py-2 bg-surface/95 backdrop-blur supports-[backdrop-filter]:bg-surface/80 border-t border-border/60 flex gap-2">
          <button type="button" class="btn-ghost flex-1 !py-2 text-sm" @click="show = false">关闭</button>
          <button type="button" class="btn-brand flex-1 !py-2 text-sm" :disabled="importing" @click="doImport">
            {{ importing ? '入库中…' : `入库 ${drafts.length} 位` }}
          </button>
        </div>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import DateField from './DateField.vue'
import Modal from './Modal.vue'
import { importTenantsBatch, parseTenantText, type ImportResult, type TenantTextDraft } from '../api/rental'
import { toast } from '../utils/toast'

const props = defineProps<{
  modelValue: boolean
  /** 已建小区的候选（供小区输入框联想），由页面从房源列表带进来 */
  communities?: string[]
}>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean]; imported: [] }>()

const show = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const text = ref('')
const parsing = ref(false)
const error = ref('')
const drafts = ref<TenantTextDraft[]>([])
const unmatched = ref<string[]>([])

const importing = ref(false)
const importError = ref('')
const importResult = ref<ImportResult | null>(null)

async function doParse() {
  if (!text.value.trim()) {
    error.value = '请先粘贴租户发来的资料'
    return
  }
  parsing.value = true
  error.value = ''
  importError.value = ''
  importResult.value = null
  try {
    const res = await parseTenantText(text.value)
    if (res.data?.code === 0) {
      drafts.value = res.data.data.drafts ?? []
      unmatched.value = res.data.data.unmatched_lines ?? []
      if (drafts.value.length === 0) error.value = '没识别到租户资料，请检查格式'
    } else {
      error.value = res.data?.message || '识别失败'
    }
  } catch {
    error.value = '识别失败，请重试'
  } finally {
    parsing.value = false
  }
}

// 去掉某一条草稿：识别多了、或某条明显不是租户资料时用。
function dropDraft(index: number) {
  drafts.value.splice(index, 1)
}

async function doImport() {
  if (drafts.value.length === 0) {
    importError.value = '没有要入库的租户'
    return
  }
  for (let i = 0; i < drafts.value.length; i++) {
    const d = drafts.value[i]
    if (!d.name?.trim()) {
      importError.value = `第 ${i + 1} 位还没填姓名`
      return
    }
    if (!d.room_no?.trim()) {
      importError.value = `第 ${i + 1} 位还没填房号（房源需先在房源管理建档）`
      return
    }
  }
  importing.value = true
  importError.value = ''
  try {
    const res = await importTenantsBatch(drafts.value.map((d) => ({ ...d })))
    if (res.data?.code === 0) {
      importResult.value = res.data.data ?? { created: 0, updated: 0, failed: 0, errors: [] }
      const done = importResult.value.created + importResult.value.updated
      if (done > 0) toast(`已入库 ${done} 位租户`)
      // 有成功的行就刷新列表并收起草稿区，剩下的失败行留在结果里供排查。
      if (done > 0) {
        emit('imported')
        drafts.value = []
        unmatched.value = []
      }
    } else {
      importError.value = res.data?.message || '入库失败'
    }
  } catch {
    importError.value = '入库失败，请重试'
  } finally {
    importing.value = false
  }
}
</script>
