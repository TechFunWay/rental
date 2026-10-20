<template>
  <Modal v-model="show" title="物品清单打印" wide @update:model-value="onShowChange">
    <div class="space-y-3">
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn-brand !py-1.5 !px-4 text-sm" :disabled="printing" @click="print">
          {{ printing ? '准备打印…' : '打印 A4' }}
        </button>
        <span class="text-[11px] text-muted-foreground">
          交房/退房时各打一份，双方在场清点后签字；清单与房间绑定，换租户也不会丢。
          <span class="sm:hidden">手机上可左右滑动查看整页。</span>
        </span>
      </div>

      <div class="max-h-[62dvh] overflow-auto rounded-xl bg-muted/40 p-2 sm:p-4">
        <!-- 预览纸张：打印根里挂的是它的克隆，所见即所印 -->
        <div ref="paperEl" class="ip-paper">
          <h1 class="ip-title">房屋物品清单</h1>

          <table class="ip-info">
            <tbody>
              <tr>
                <th>房屋</th>
                <td>{{ roomLabel || '　' }}</td>
                <th>日期</th>
                <td>{{ printDate }}</td>
              </tr>
              <tr>
                <th>出租方（甲方）</th>
                <td>{{ landlordName || '　' }}</td>
                <th>承租方（乙方）</th>
                <td>{{ tenantName || '　' }}</td>
              </tr>
            </tbody>
          </table>

          <!-- 明细表：按固定顺序列出，空清单也打表头（现场手写补录时可用） -->
          <table class="ip-table">
            <thead>
              <tr>
                <th class="w-[8%]">序号</th>
                <th class="w-[30%]">物品名称</th>
                <th class="w-[10%]">数量</th>
                <th class="w-[16%]">状况</th>
                <th class="w-[16%]">归属</th>
                <th class="w-[20%]">备注</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(it, i) in sortedItems" :key="it.id">
                <td class="text-center">{{ i + 1 }}</td>
                <td>{{ it.name }}</td>
                <td class="text-center">{{ it.quantity }}</td>
                <td>{{ it.condition || '' }}</td>
                <td>{{ it.builtin ? '房东提供' : '租客自带' }}</td>
                <td>{{ it.note || '' }}</td>
              </tr>
              <!-- 不足 12 行补空行：方便手写临时增减，也让纸张不显得空 -->
              <tr v-for="n in blankRows" :key="`blank-${n}`">
                <td class="text-center">{{ sortedItems.length + n }}</td>
                <td></td>
                <td></td>
                <td></td>
                <td></td>
                <td></td>
              </tr>
            </tbody>
          </table>

          <p class="ip-total">
            共 {{ sortedItems.length }} 项，合计 {{ totalQuantity }} 件；清单经双方当场核对无误后签字确认。
          </p>

          <div class="ip-sign">
            <div>
              <p>甲方（签字）：</p>
              <p class="ip-sign-line"></p>
              <p>日期：　　　年　　月　　日</p>
            </div>
            <div>
              <p>乙方（签字）：</p>
              <p class="ip-sign-line"></p>
              <p>日期：　　　年　　月　　日</p>
            </div>
          </div>

          <p class="ip-foot">
            退房时按本清单逐项核对：物品缺失或非自然损耗损坏的，由乙方照价赔偿或从押金中扣除。
          </p>
        </div>
      </div>

      <div class="flex gap-3 pt-1">
        <button type="button" class="btn-ghost flex-1" @click="show = false">关闭</button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
/**
 * ItemListPrintModal — 物品清单 A4 打印（2026-10-20 粉丝需求「还有一张物品清单」）。
 *
 * 物品台账（RoomItemsCard）此前只能屏幕上看，交房/退房要纸质清单对照签字时只能
 * 手抄。这里把它排版成 A4 表格：序号/名称/数量/状况/归属/备注 + 签字栏，不足
 * 12 行补空行方便现场手写增减。
 *
 * 为什么不带照片：交接清单是双方签字确认的文书，照片打印在 A4 上会挤成一片且
 * 黑白打印基本看不清；照片仍在台账里看（手机端拍、退租时逐项核）。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Modal from './Modal.vue'
import type { RoomItemView } from '../api/rental'
import { appendPrintPage } from '../utils/print-root'
import { applyA4Portrait, printA4, removeA4Portrait } from '../utils/print-kit'

const props = withDefaults(defineProps<{
  modelValue: boolean
  roomLabel?: string
  landlordName?: string
  tenantName?: string
  items: RoomItemView[]
}>(), { roomLabel: '', landlordName: '', tenantName: '' })

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const show = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const paperEl = ref<HTMLElement | null>(null)
const printing = ref(false)
let removeStaged: (() => void) | null = null

const printDate = computed(() => {
  const d = new Date()
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`
})

/** 打印顺序：房东提供的在前、按 sort 再按 id，与屏幕上的列表顺序一致。 */
const sortedItems = computed(() =>
  [...props.items].sort(
    (a, b) => Number(b.builtin) - Number(a.builtin) || a.sort - b.sort || a.id - b.id,
  ),
)

const totalQuantity = computed(() => sortedItems.value.reduce((s, it) => s + Number(it.quantity || 0), 0))
const blankRows = computed(() => Math.max(0, 12 - sortedItems.value.length))

async function stagePaper() {
  await nextTick()
  removeStaged?.()
  removeStaged = null
  if (!paperEl.value) return
  removeStaged = appendPrintPage(paperEl.value)
}

function unstagePaper() {
  removeStaged?.()
  removeStaged = null
}

function onShowChange(v: boolean) {
  if (!v) {
    unstagePaper()
    removeA4Portrait()
    emit('update:modelValue', false)
  }
}

async function print() {
  printing.value = true
  try {
    applyA4Portrait()
    await stagePaper()
    await new Promise((r) => requestAnimationFrame(() => r(null)))
    printA4()
  } finally {
    printing.value = false
  }
}

watch(() => props.modelValue, async (open) => {
  if (open) await stagePaper()
})

onBeforeUnmount(() => {
  unstagePaper()
  removeA4Portrait()
})
</script>

<style scoped>
.ip-paper {
  box-sizing: border-box;
  width: 100%;
  max-width: 760px;
  margin: 0 auto;
  padding: 16mm;
  background: #fff;
  color: #111;
  font-family: 'Songti SC', 'SimSun', 'Noto Serif CJK SC', serif;
  font-size: 12.5px;
  line-height: 1.8;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.12);
}
/* 手机端：纸张保持接近 A4 的版式宽度，交给外层 overflow-auto 横向滚动查看。
   若跟着容器缩到 375px，六列表格会被压成一字一行（2026-10-20 手机上实测），
   预览虽能滚动，但「所见即所印」就失真了。 */
@media (max-width: 640px) {
  .ip-paper {
    width: 720px;
    max-width: none;
    margin: 0;
  }
}
.ip-title {
  margin: 0 0 8mm;
  text-align: center;
  font-size: 20px;
  font-weight: 700;
  letter-spacing: 0.2em;
}
.ip-info {
  width: 100%;
  border-collapse: collapse;
  margin-bottom: 4mm;
}
.ip-info th,
.ip-info td {
  border: 1px solid #999;
  padding: 1.8mm 2.5mm;
  text-align: left;
}
.ip-info th {
  width: 18%;
  font-weight: 400;
  background: #f7f7f7;
  white-space: nowrap;
}
.ip-table {
  width: 100%;
  border-collapse: collapse;
}
.ip-table th,
.ip-table td {
  border: 1px solid #999;
  padding: 1.6mm 2mm;
  vertical-align: top;
}
.ip-table th {
  background: #f2f2f2;
  font-weight: 600;
  text-align: center;
  white-space: nowrap;
}
.ip-table tr {
  /* 打印时避免一行被分页撕开 */
  break-inside: avoid;
}
.ip-total {
  margin-top: 3mm;
  font-size: 12px;
}
.ip-sign {
  display: flex;
  justify-content: space-between;
  gap: 10mm;
  margin-top: 12mm;
}
.ip-sign > div {
  flex: 1;
}
.ip-sign-line {
  height: 12mm;
  border-bottom: 1px solid #333;
}
.ip-foot {
  margin-top: 8mm;
  font-size: 11.5px;
  color: #444;
}

@media print {
  .ip-paper {
    max-width: none;
    padding: 0;
    box-shadow: none;
  }
}
</style>
