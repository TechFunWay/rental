<template>
  <Modal v-model="show" title="合同打印" wide @update:model-value="onShowChange">
    <div class="space-y-3">
      <!-- 模式切换：模板合同（现场生成）/ 已上传合同（拍照/扫描件） -->
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="opt in modeOptions" :key="opt.value" type="button"
          class="px-3 py-1 rounded-full text-xs border transition-colors"
          :class="mode === opt.value
            ? 'border-indigo-500 bg-indigo-500/10 text-indigo-500 font-semibold'
            : 'border-border text-muted-foreground hover:text-foreground'"
          @click="mode = opt.value"
        >{{ opt.label }}</button>
      </div>

      <!-- 模板合同：选模板 → 预览 → 打印 -->
      <template v-if="mode === 'template'">
        <div class="flex flex-wrap items-center gap-2">
          <select v-model="template" class="input-field !py-1.5 !w-auto text-sm">
            <option v-for="t in templateOptions" :key="t.value" :value="t.value">{{ t.label }}</option>
          </select>
          <button type="button" class="btn-brand !py-1.5 !px-4 text-sm" :disabled="printing" @click="printTemplate">
            {{ printing ? '准备打印…' : '打印 A4' }}
          </button>
          <span class="text-[11px] text-muted-foreground">
            打印时只输出下面的合同正文；先核对信息再打印，签字后与租户各留一份。
            <span class="sm:hidden">手机上可左右滑动查看整页。</span>
          </span>
        </div>

        <div class="max-h-[62dvh] overflow-auto rounded-xl bg-muted/40 p-2 sm:p-4">
          <!-- 预览纸张：与打印输出同一份 DOM（挂到打印根的是它的克隆），所见即所印 -->
          <div ref="paperEl" class="cp-paper">
            <!-- 标题 -->
            <h1 class="cp-title">{{ template === 'renewal' ? '房屋租赁续租协议' : '房屋租赁合同' }}</h1>

            <!-- 双方信息 -->
            <table class="cp-info">
              <tbody>
                <tr>
                  <th>出租方（甲方）</th>
                  <td>{{ landlordName }}</td>
                  <th>联系电话</th>
                  <td>{{ landlordPhone }}</td>
                </tr>
                <tr>
                  <th>承租方（乙方）</th>
                  <td>{{ tenant?.name || '' }}</td>
                  <th>联系电话</th>
                  <td>{{ tenant?.phone || '' }}</td>
                </tr>
                <tr>
                  <th>身份证号</th>
                  <td colspan="3">{{ tenant?.id_card || '　' }}</td>
                </tr>
                <tr v-if="tenant?.address">
                  <th>身份证住址</th>
                  <td colspan="3">{{ tenant.address }}</td>
                </tr>
              </tbody>
            </table>

            <!-- 房屋信息 -->
            <table class="cp-info">
              <tbody>
                <tr>
                  <th>房屋坐落</th>
                  <td colspan="3">{{ roomPlace }}</td>
                </tr>
                <tr>
                  <th>建筑面积</th>
                  <td>{{ areaText }}</td>
                  <th>用途</th>
                  <td>居住</td>
                </tr>
              </tbody>
            </table>

            <!-- 租金与租期（续租协议只写续租段） -->
            <table class="cp-info">
              <tbody>
                <tr>
                  <th>月租金</th>
                  <td>{{ money(rentNow) }} 元</td>
                  <th>缴费周期</th>
                  <td>{{ cycleLabel(tenant?.pay_cycle) }}</td>
                </tr>
                <tr>
                  <th>押金</th>
                  <td>{{ tenant?.deposit ? money(tenant.deposit) + ' 元' : '　' }}</td>
                  <th>租期</th>
                  <td>{{ leasePeriodText }}</td>
                </tr>
                <tr v-if="template === 'renewal'">
                  <th>本次续租</th>
                  <td colspan="3">{{ renewalSummary }}</td>
                </tr>
              </tbody>
            </table>

            <!-- 条款 -->
            <ol class="cp-clauses">
              <li v-for="(c, i) in clauses" :key="i">{{ c }}</li>
            </ol>

            <!-- 签字栏 -->
            <div class="cp-sign">
              <div>
                <p>甲方（签字／盖章）：</p>
                <p class="cp-sign-line"></p>
                <p>日期：　　　年　　月　　日</p>
              </div>
              <div>
                <p>乙方（签字）：</p>
                <p class="cp-sign-line"></p>
                <p>日期：　　　年　　月　　日</p>
              </div>
            </div>
            <p class="cp-foot">本协议一式两份，甲乙双方各执一份，自双方签字之日起生效。</p>
          </div>
        </div>
      </template>

      <!-- 已上传合同：图片逐页打印、PDF 直接打印 -->
      <template v-else>
        <p class="text-[11px] text-muted-foreground">
          已上传的合同（拍照/扫描件）按原文件打印：图片每张一页，PDF 走浏览器自带分页。
        </p>
        <p v-if="!contracts.length" class="text-xs text-muted-foreground py-2">这个租户还没有上传合同。</p>
        <ul v-else class="divide-y divide-border rounded-xl border border-border">
          <li v-for="c in contracts" :key="c.id" class="flex items-center gap-2 px-3 py-2 text-xs">
            <span class="truncate flex-1 text-foreground" :title="c.file_name">{{ c.file_name }}</span>
            <span class="text-muted-foreground shrink-0">{{ fileSizeText(c.file_size) }}</span>
            <button type="button" class="text-brand-600 dark:text-brand-300 hover:underline shrink-0" @click="printUploaded(c)">
              打印
            </button>
          </li>
        </ul>
      </template>

      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <div class="flex gap-3 pt-1">
        <button type="button" class="btn-ghost flex-1" @click="show = false">关闭</button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
/**
 * ContractPrintModal — 合同打印（2026-10-20 粉丝需求「合同打印」）。
 *
 * 两条路：
 *  1. 模板合同：租房合同/续租协议的常见条款就地生成 A4 文档，房东填完租户与房源
 *     信息就能直接打出来签字——不用再自己找 Word 模板改。
 *  2. 已上传合同：把拍照/扫描的合同按原文件打印（图片逐张一页、PDF 走浏览器分页）。
 *
 * 为什么不用 window.print() 直接打弹窗：print-root 管线只在 #print-root 里打印
 * 克隆页，能避开弹窗遮罩和页面其余内容（2026-10-11 粉丝反馈「打印能显示网页」的
 * 坑）。所以纸张元素要在挂载时把克隆挂进 #print-root，打印时只切纸张。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import { fetchContractBlob, getContracts, type Contract, type RoomView, type Tenant } from '../api/rental'
import { getTenantRenewals, type LeaseRenewal } from '../api/renew'
import { cycleLabel } from '../utils/cycle'
import { appendPrintPage } from '../utils/print-root'
import { applyA4Portrait, printA4, printImageBlobs, printPdfBlob, removeA4Portrait } from '../utils/print-kit'

const props = withDefaults(defineProps<{
  modelValue: boolean
  tenant: Tenant | null
  /** 租户所在的房源（甲方信息、地址、面积、租金都从这里取） */
  room?: RoomView | null
}>(), { room: null })

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const show = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

type Mode = 'template' | 'uploaded'
const mode = ref<Mode>('template')
const modeOptions: { value: Mode; label: string }[] = [
  { value: 'template', label: '模板合同' },
  { value: 'uploaded', label: '已上传合同' },
]

type Template = 'lease' | 'renewal'
const template = ref<Template>('lease')
const templateOptions: { value: Template; label: string }[] = [
  { value: 'lease', label: '房屋租赁合同' },
  { value: 'renewal', label: '续租协议' },
]

const paperEl = ref<HTMLElement | null>(null)
const printing = ref(false)
const error = ref('')
const contracts = ref<Contract[]>([])
const renewals = ref<LeaseRenewal[]>([])
let removeStaged: (() => void) | null = null

const landlordName = computed(() => props.room?.landlord_name || '（出租方姓名）')
const landlordPhone = computed(() => props.room?.landlord_phone || '（联系电话）')

/** 房源展示：小区/楼栋-房号 + 定位地址（2026-10-20 新增字段，为空则省略）。 */
const roomPlace = computed(() => {
  const r = props.room
  if (!r) return '（房源信息未加载）'
  const parts = [r.community, r.building, r.unit, r.floor].filter(Boolean)
  const label = [...parts, r.room_no].filter(Boolean).join(' ')
  return r.address ? `${label}（${r.address}）` : label
})

const areaText = computed(() => {
  const a = Number(props.room?.area_size || 0)
  return a > 0 ? `${a} ㎡` : '　'
})

const rentNow = computed(() => Number(props.room?.default_rent || 0))

const leasePeriodText = computed(() => {
  const t = props.tenant
  if (!t) return '　'
  if (t.move_in_date && t.lease_end_date) return `${t.move_in_date} 至 ${t.lease_end_date}`
  return t.lease_end_date ? `至 ${t.lease_end_date}` : '　'
})

/** 最近一次续租（续租协议用它说明「从哪续到哪、租金怎么变」）。 */
const lastRenewal = computed(() => renewals.value[0] ?? null)
const renewalSummary = computed(() => {
  const r = lastRenewal.value
  if (!r) return '（无续租记录，按上方租期执行）'
  const rentPart = r.rent_before !== r.rent_after
    ? `续租后月租金 ${money(r.rent_after)} 元（原 ${money(r.rent_before)} 元）`
    : `月租金不变 ${money(r.rent_after)} 元`
  return `${r.from_end_date || '未约定'} 起续租 ${r.months} 个月，至 ${r.to_end_date}；${rentPart}`
})

/** 条款：合同给完整条款，续租协议只强调续租相关义务。 */
const clauses = computed(() => {
  const rent = money(rentNow.value)
  const area = areaText.value.trim()
  if (template.value === 'renewal') {
    const r = lastRenewal.value
    return [
      `甲乙双方就上述房屋原租赁合同的续租事宜达成如下协议，原合同其余条款继续有效。`,
      `本次续租期限为 ${r?.months ?? '　'} 个月，续租后租期届满日为 ${r?.to_end_date || '　'}。`,
      `续租期间月租金为 ${money(r?.rent_after ?? rentNow.value)} 元，${cycleLabel(props.tenant?.pay_cycle)}结算，乙方应按期足额支付。`,
      `押金按原合同执行${props.tenant?.deposit ? `（${money(props.tenant.deposit)} 元）` : ''}，续租期间不作调整；租期届满且无违约、无欠费时全额退还。`,
      `续租期间水、电、燃气、物业等费用由乙方按实际发生额承担；房屋及原有设施由乙方妥善使用，非自然损耗的损坏由乙方修复或赔偿。`,
      `任何一方不再续租的，应提前一个月书面通知对方；乙方需在租期届满前腾空并结清费用。`,
      `本协议作为原租赁合同的补充，与原合同具有同等法律效力；未尽事宜由双方协商解决。`,
    ]
  }
  return [
    `甲方将上述房屋出租给乙方作居住使用，乙方不得擅自改变房屋用途或转租、分租给他人。`,
    `租赁期限：${leasePeriodText.value === '　' ? '自　年　月　日起至　年　月　日止' : leasePeriodText.value}。租期届满，乙方需续租的应提前一个月与甲方协商。`,
    `月租金为 ${rent} 元，按${cycleLabel(props.tenant?.pay_cycle)}支付，乙方应于每个缴费周期首月${props.tenant?.pay_day ? `${props.tenant.pay_day} 日前` : '约定日期前'}付清；逾期超过十五日的，甲方有权解除合同并从押金中扣除相应损失。`,
    `乙方于签订本合同时向甲方交付押金${props.tenant?.deposit ? ` ${money(props.tenant.deposit)} 元` : '（见双方约定）'}，租期届满、乙方结清费用并将房屋恢复原状后，甲方应全额退还押金。`,
    `租赁期间的水、电、燃气、卫生、物业等费用由乙方承担，按房屋现有计量表具或管理部门通知金额结算。`,
    `房屋及附属设施、家具家电（详见《物品清单》）由甲方交付乙方使用，乙方应妥善保管、合理使用；因乙方使用不当造成损坏的，由乙方负责修复或赔偿。`,
    `乙方不得擅自装修或改变房屋结构；确需装饰装修的，应事先取得甲方书面同意。`,
    `任何一方提前解除合同的，应提前一个月书面通知对方；甲方因自身原因提前收回房屋的，应退还剩余租金并赔偿乙方相应损失。`,
    `本合同未尽事宜，由双方协商补充；补充条款与本合同具有同等效力。本合同一式两份，甲乙双方各执一份，签字后生效。`,
    area ? `双方确认房屋建筑面积约 ${area}，以实际交付状态为准。` : '',
  ].filter(Boolean)
})

function money(v: number | undefined | null) {
  return Number(v || 0).toFixed(2).replace(/\.00$/, '')
}

function fileSizeText(bytes: number) {
  if (!bytes) return '—'
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/** 把预览纸张的克隆挂进打印根；换模板/重开时先撤旧的，避免残留旧合同。 */
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

async function printTemplate() {
  error.value = ''
  printing.value = true
  try {
    // A4 规则只在打印瞬间注入：弹窗开着时被别的打印（收费单据）抢走 @page 也不影响。
    applyA4Portrait()
    await stagePaper()
    await new Promise((r) => requestAnimationFrame(() => r(null)))
    printA4()
  } catch (e) {
    error.value = (e as Error)?.message || '打印失败，请重试'
  } finally {
    printing.value = false
  }
}

async function printUploaded(c: Contract) {
  error.value = ''
  try {
    const isPdf = (c.mime_type || '').includes('pdf') || c.file_name.toLowerCase().endsWith('.pdf')
    const isImg = (c.mime_type || '').startsWith('image/')
    const blob = await fetchContractBlob(c.id)
    if (isPdf) {
      await printPdfBlob(blob)
    } else if (isImg) {
      await printImageBlobs([blob])
    } else {
      toast('该文件类型不支持直接打印，请下载后打开打印', 'error')
    }
  } catch (e) {
    const msg = (e as { response?: { data?: { message?: string } } })?.response?.data?.message
    error.value = msg || (e as Error)?.message || '打印失败，请重试'
  }
}

/** 拉合同列表与续租历史：两个入口都要（模板要续租信息、上传页要文件列表）。 */
async function loadAux() {
  const t = props.tenant
  if (!t) return
  try {
    const res = await getContracts(t.id)
    // 后端 GET /tenants/:id/contracts 的 data 直接是数组（不分页，每租户上限 20 份）。
    contracts.value = (res.data?.data ?? []) as Contract[]
  } catch {
    contracts.value = []
  }
  try {
    const res = await getTenantRenewals(t.id)
    renewals.value = (res.data?.data?.items ?? []) as LeaseRenewal[]
  } catch {
    renewals.value = []
  }
}

watch(() => props.modelValue, async (open) => {
  if (!open) return
  error.value = ''
  mode.value = 'template'
  await loadAux()
  // 续过租的租户默认给续租协议：房东打开这个弹窗时，十有八九是要续签而不是新签。
  template.value = renewals.value.length ? 'renewal' : 'lease'
  await stagePaper()
})

function onAfterPrint() {
  // 打印结束只撤 A4 规则，纸张克隆留着——用户可能连打两份。
  removeA4Portrait()
}

watch(template, async () => {
  if (props.modelValue && mode.value === 'template') await stagePaper()
})

watch(mode, async () => {
  if (mode.value === 'template') await stagePaper()
  else unstagePaper()
})

// afterprint 监听只在挂载时挂一次：每次打开都挂会在重复开关弹窗后累积一堆
// 同样的回调（打印结束时挨个跑 removeA4Portrait，虽然幂等但没必要）。
onMounted(() => {
  document.addEventListener('afterprint', onAfterPrint)
})

onBeforeUnmount(() => {
  unstagePaper()
  removeA4Portrait()
  document.removeEventListener('afterprint', onAfterPrint)
})
</script>

<style scoped>
/* A4 纸张排版：屏幕上按 A4 比例预览，打印时由 print-root 隔离页面，
   这里只负责纸张自身的黑字白底与字号（不消费主题变量，深色主题下也是白纸黑字）。 */
.cp-paper {
  box-sizing: border-box;
  width: 100%;
  max-width: 760px;
  margin: 0 auto;
  padding: 18mm 16mm;
  background: #fff;
  color: #111;
  font-family: 'Songti SC', 'SimSun', 'Noto Serif CJK SC', serif;
  font-size: 12.5px;
  line-height: 1.9;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.12);
}
/* 手机端：纸张保持接近 A4 的版式宽度，交给外层 overflow-auto 横向滚动查看。
   跟着容器缩到 375px 时，双方信息表会被压成一字一行，预览就与打印结果不符。 */
@media (max-width: 640px) {
  .cp-paper {
    width: 720px;
    max-width: none;
    margin: 0;
  }
}
.cp-title {
  margin: 0 0 10mm;
  text-align: center;
  font-size: 20px;
  font-weight: 700;
  letter-spacing: 0.2em;
}
.cp-info {
  width: 100%;
  border-collapse: collapse;
  margin-bottom: 4mm;
}
.cp-info th,
.cp-info td {
  border: 1px solid #999;
  padding: 2mm 2.5mm;
  text-align: left;
  vertical-align: top;
}
.cp-info th {
  width: 22%;
  font-weight: 400;
  color: #333;
  background: #f7f7f7;
  white-space: nowrap;
}
.cp-clauses {
  margin: 4mm 0 0;
  padding-left: 6mm;
}
.cp-clauses li {
  margin-bottom: 1.6mm;
}
.cp-sign {
  display: flex;
  justify-content: space-between;
  gap: 10mm;
  margin-top: 14mm;
}
.cp-sign > div {
  flex: 1;
}
.cp-sign-line {
  height: 12mm;
  border-bottom: 1px solid #333;
}
.cp-foot {
  margin-top: 10mm;
  font-size: 11.5px;
  color: #444;
}

@media print {
  .cp-paper {
    max-width: none;
    padding: 0;
    box-shadow: none;
  }
}
</style>
