<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="抄表账单" description="月度抄表、费用自动计算、收款与欠缴跟踪、收费单据">
      <template #actions>
        <button class="btn-ghost" @click="downloadTemplate">下载模板</button>
        <button class="btn-ghost" @click="doExport">导出</button>
        <button class="btn-ghost" @click="showImport = true">导入</button>
        <button class="btn-brand" @click="openReading()">📝 抄表录入</button>
      </template>
    </PageHeader>

    <!-- 筛选 -->
    <div class="surface rounded-2xl p-4 flex flex-wrap items-center gap-3">
      <input v-model="period" class="input-field !py-2 !w-[150px] tabular-nums" type="month" @change="load(1)" />
      <select v-model="status" class="input-field !py-2 !w-auto" @change="load(1)">
        <option value="">全部状态</option>
        <option value="arrears">欠缴（含部分）</option>
        <option value="unpaid">未缴纳</option>
        <option value="partial">部分已缴</option>
        <option value="paid">已缴清</option>
      </select>
      <input v-model="keyword" class="input-field flex-1 min-w-[180px] !py-2" placeholder="搜索房号或租户…" @keyup.enter="load(1)" />
      <button class="btn-brand !py-2" @click="load(1)">查询</button>
      <div class="ml-auto text-sm text-muted-foreground whitespace-nowrap">
        本页合计：应收 <strong class="text-foreground tabular-nums">{{ fmtMoney(pageSum.total) }}</strong> 元 ·
        欠缴 <strong class="text-rose-500 tabular-nums">{{ fmtMoney(pageSum.arrears) }}</strong> 元
      </div>
    </div>

    <!-- 账单表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="bills.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        {{ period }} 暂无账单。点击右上角「抄表录入」选房间抄表，保存后账单按读数自动生成。
        <div class="mt-3"><button class="btn-ghost !py-1.5" @click="openReading()">📝 去抄表</button></div>
      </div>
      <template v-else>
      <!-- 桌面端：全字段表格 -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm min-w-[1200px]">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th rowspan="2" class="py-2.5 px-3 font-medium align-bottom">月份</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium align-bottom">房号</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium align-bottom">租户</th>
              <th colspan="6" class="py-2 px-3 font-medium text-center border-b border-border/60">抄数（上月 → 本月）</th>
              <th colspan="3" class="py-2 px-3 font-medium text-center border-b border-border/60">抄表费用</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">卫生费</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">管理费</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">本月租金</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">已收</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">欠缴额</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium align-bottom">状态</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">操作</th>
            </tr>
            <tr class="text-left text-muted-foreground/80 text-xs border-b border-border">
              <th class="py-1.5 px-3 font-medium text-right">水表</th>
              <th class="py-1.5 px-3 font-medium text-right">电表</th>
              <th class="py-1.5 px-3 font-medium text-right">燃气表</th>
              <th class="py-1.5 px-3 font-medium text-right">水费</th>
              <th class="py-1.5 px-3 font-medium text-right">电费</th>
              <th class="py-1.5 px-3 font-medium text-right">燃气费</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="b in bills" :key="b.id" class="hover:bg-muted/30 transition-colors" :class="isArrears(b) ? 'bg-rose-500/[0.03]' : ''">
              <td class="py-2.5 px-3 text-muted-foreground tabular-nums whitespace-nowrap">{{ b.period }}</td>
              <td class="py-2.5 px-3 font-semibold text-foreground whitespace-nowrap">{{ b.room_no }}</td>
              <td class="py-2.5 px-3 text-foreground whitespace-nowrap max-w-[120px] truncate" :title="b.tenant_name">{{ b.tenant_name || '—' }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ fmtRead(b.water_last) }} → {{ fmtRead(b.water_now) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ fmtRead(b.elec_last) }} → {{ fmtRead(b.elec_now) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ fmtRead(b.gas_last) }} → {{ fmtRead(b.gas_now) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.water_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.elec_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.gas_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.sanitation_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.management_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums font-bold text-foreground">{{ fmtMoney(b.total_amount) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(b.paid_amount) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums" :class="isArrears(b) ? 'text-rose-500 font-semibold' : ''">{{ fmtMoney(arrearsOf(b)) }}</td>
              <td class="py-2.5 px-3">
                <span class="badge" :class="statusClass(b)">{{ statusText(b) }}</span>
              </td>
              <td class="py-2.5 px-3 text-right whitespace-nowrap">
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-2.5" @click="openReading(b)">抄表</button>
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-2.5" @click="openReceipt(b)">单据</button>
                <button class="text-foreground hover:underline mr-2.5" @click="openEdit(b)">编辑</button>
                <button v-if="isArrears(b)" class="text-emerald-600 dark:text-emerald-400 hover:underline mr-2.5" @click="openPay(b)">收款</button>
                <button class="text-destructive hover:underline" @click="remove(b)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表（紧凑版） -->
      <div class="md:hidden divide-y divide-border">
        <div v-for="b in bills" :key="b.id" class="px-3 py-2.5 space-y-1.5" :class="isArrears(b) ? 'bg-rose-500/[0.04]' : ''">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <div class="flex items-center gap-1.5">
                <span class="text-sm font-semibold text-foreground">{{ b.room_no }}</span>
                <span class="text-[11px] text-muted-foreground tabular-nums">{{ b.period }}</span>
                <span class="text-[11px] text-muted-foreground truncate">{{ b.tenant_name || '—' }}</span>
              </div>
            </div>
            <span class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]" :class="statusClass(b)">{{ statusText(b) }}</span>
          </div>

          <div class="text-[11px] text-muted-foreground tabular-nums rounded-md bg-muted/50 px-2 py-1.5">
            <div>水 {{ fmtRead(b.water_last) }}→{{ fmtRead(b.water_now) }} 电 {{ fmtRead(b.elec_last) }}→{{ fmtRead(b.elec_now) }} 气 {{ fmtRead(b.gas_last) }}→{{ fmtRead(b.gas_now) }}</div>
            <div class="mt-0.5">水费 {{ fmtMoney(b.water_fee) }} · 电费 {{ fmtMoney(b.elec_fee) }} · 燃气 {{ fmtMoney(b.gas_fee) }} · 卫生 {{ fmtMoney(b.sanitation_fee) }} · 管理 {{ fmtMoney(b.management_fee) }}</div>
          </div>

          <div class="flex items-end justify-between gap-2">
            <div class="text-[11px] text-muted-foreground">
              租金 <span class="text-sm font-bold text-foreground tabular-nums">{{ fmtMoney(b.total_amount) }}</span>
            </div>
            <div class="text-[11px] text-right space-y-0">
              <div class="text-emerald-600 dark:text-emerald-400 tabular-nums">已收 {{ fmtMoney(b.paid_amount) }}</div>
              <div class="tabular-nums" :class="isArrears(b) ? 'text-rose-500 font-semibold' : 'text-muted-foreground'">欠缴 {{ fmtMoney(arrearsOf(b)) }}</div>
            </div>
            <div class="flex items-center gap-3 text-xs border-l border-border/60 pl-3">
              <button class="text-brand-600 dark:text-brand-300" @click="openReading(b)">抄表</button>
              <button class="text-brand-600 dark:text-brand-300" @click="openReceipt(b)">单据</button>
              <button class="text-foreground" @click="openEdit(b)">编辑</button>
              <button v-if="isArrears(b)" class="text-emerald-600 dark:text-emerald-400" @click="openPay(b)">收款</button>
              <button class="text-destructive" @click="remove(b)">删除</button>
            </div>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 编辑账单 -->
    <Modal v-model="showEdit" title="编辑账单" >
      <form v-if="editForm" class="space-y-4" @submit.prevent="saveEdit">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">账期</label>
            <input v-model="editForm.period" class="input-field tabular-nums" placeholder="2026-09" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">租户名</label>
            <input v-model="editForm.tenant_name" class="input-field" />
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">租金（元）</label>
          <input v-model.number="editForm.rent" type="number" step="0.01" min="0" class="input-field" />
        </div>
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <p class="text-xs font-semibold text-muted-foreground">抄表（上月 / 本月读数与单价）</p>
          <div v-for="m in meterBlocks" :key="m.lastKey" class="rounded-lg bg-muted/60 p-2.5">
            <div class="flex items-center justify-between mb-1.5">
              <span class="text-sm font-medium text-foreground">{{ m.label }}</span>
              <span class="text-xs tabular-nums text-muted-foreground">
                费用预估 <strong class="text-foreground">{{ fmtMoney(previewFee(editForm[m.lastKey], editForm[m.nowKey], editForm[m.priceKey])) }}</strong>
              </span>
            </div>
            <div class="grid grid-cols-3 gap-2">
              <label class="block">
                <span class="text-[11px] text-muted-foreground">上月读数</span>
                <input v-model.number="editForm[m.lastKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">本月读数</span>
                <input v-model.number="editForm[m.nowKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价</span>
                <input v-model.number="editForm[m.priceKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
            </div>
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">卫生费（元）</label>
            <input v-model.number="editForm.sanitation_fee" type="number" step="0.01" min="0" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">管理费（元）</label>
            <input v-model.number="editForm.management_fee" type="number" step="0.01" min="0" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">已收金额（元）</label>
            <input v-model.number="editForm.paid_amount" type="number" step="0.01" min="0" class="input-field" />
          </div>
        </div>
        <div class="rounded-xl bg-muted p-3 text-sm flex items-center justify-between">
          <span class="text-muted-foreground">应付合计（服务端按同口径重算）</span>
          <strong class="text-foreground tabular-nums">{{ fmtMoney(previewTotal()) }} 元</strong>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <input v-model="editForm.remark" class="input-field" placeholder="选填" />
        </div>
        <p v-if="editError" class="text-sm text-destructive">{{ editError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showEdit = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 抄表录入 -->
    <Modal v-model="showReading" title="抄表录入">
      <form class="space-y-4" @submit.prevent="saveReading">
        <!-- 独立表单：自选账期 + 全部在租房间，未建账的保存时自动生成 -->
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">账期</label>
            <input v-model="readingPeriod" type="month" class="input-field tabular-nums" @change="loadReadingData" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">房间（在租）</label>
            <select
              :value="readingBillId || readingRoomId"
              class="input-field"
              :disabled="readingLoading || readingCandidates.length === 0"
              @change="onReadingPick($event)"
            >
              <option v-if="readingCandidates.length === 0" :value="0" disabled>暂无在租房间</option>
              <option v-for="c in readingCandidates" :key="c.bill?.id ?? c.room.id" :value="c.bill?.id ?? c.room.id">
                {{ c.bill ? '✓ ' : '' }}{{ c.room.room_no }} · {{ c.bill?.tenant_name || c.room.current_tenants?.map((t) => t.name).join('、') || '—' }}
              </option>
            </select>
          </div>
        </div>
        <div v-if="readingCandidates.length" class="text-xs text-muted-foreground">
          本月已抄 <strong class="text-foreground tabular-nums">{{ readingBillCount }}</strong> / {{ readingCandidates.length }} 间，保存后自动跳下一间未抄的。
        </div>

        <div v-if="readingLoading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>

        <div v-else-if="readingCandidates.length === 0" class="py-6 text-center">
          <p class="text-sm text-muted-foreground">没有在租房间。请先到「房源管理」添加房源并登记租户。</p>
        </div>

        <p v-else-if="readingBillCount === 0" class="text-xs text-muted-foreground bg-muted/60 rounded-lg px-3 py-2">
          {{ readingPeriod }} 这些房间还没建账，直接抄表保存即可，账单会按读数自动生成。
        </p>

        <template v-else-if="readingForm">
          <div class="rounded-xl border border-border p-2.5 sm:p-3.5 space-y-2 sm:space-y-3">
            <p class="text-xs font-semibold text-muted-foreground">录入本月读数（上月读数自动带入）</p>
            <div v-for="m in meterBlocks" :key="m.lastKey" class="rounded-lg bg-muted/60 px-2.5 py-2">
              <div class="flex items-center justify-between mb-1">
                <span class="text-sm font-medium text-foreground">{{ m.label }}</span>
                <span class="text-[11px] tabular-nums text-muted-foreground">
                  费用 <strong class="text-foreground">{{ fmtMoney(previewFee(readingForm[m.lastKey], readingForm[m.nowKey], readingForm[m.priceKey])) }}</strong>
                </span>
              </div>
              <!-- 手机端两列（本月读数为主），桌面端三列 -->
              <div class="grid grid-cols-2 sm:grid-cols-3 gap-1.5 sm:gap-2 items-end">
                <div class="hidden sm:block">
                  <span class="text-[11px] text-muted-foreground block">上月读数</span>
                  <div class="input-field !px-2 !py-1.5 text-right bg-muted/80 text-muted-foreground tabular-nums">{{ fmtRead(readingForm[m.lastKey]) }}</div>
                </div>
                <label class="block">
                  <span class="text-[11px] text-muted-foreground block sm:hidden">本月（上月 {{ fmtRead(readingForm[m.lastKey]) }}）</span>
                  <span class="hidden text-[11px] text-muted-foreground">本月读数 *</span>
                  <input v-model.number="readingForm[m.nowKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
                </label>
                <label class="block">
                  <span class="text-[11px] text-muted-foreground">单价</span>
                  <input v-model.number="readingForm[m.priceKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
                </label>
              </div>
            </div>
          </div>
          <div class="rounded-xl bg-muted p-3 text-sm flex items-center justify-between">
            <span class="text-muted-foreground">应付合计（含租金与其他费用）</span>
            <strong class="text-foreground tabular-nums">{{ fmtMoney(readingTotal()) }} 元</strong>
          </div>
        </template>

        <p v-if="readingError" class="text-sm text-destructive">{{ readingError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showReading = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving || !readingForm">{{ saving ? '保存中…' : '保存读数' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 收款 -->
    <Modal v-model="showPay" title="登记收款">
      <form v-if="payBillRow" class="space-y-4" @submit.prevent="savePay">
        <div class="rounded-xl bg-muted p-4 text-sm space-y-1.5">
          <div class="flex justify-between"><span class="text-muted-foreground">房号 / 租户</span><span class="font-semibold text-foreground">{{ payBillRow.room_no }} · {{ payBillRow.tenant_name || '—' }}</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">应付合计</span><span class="tabular-nums">{{ fmtMoney(payBillRow.total_amount) }} 元</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">已收</span><span class="tabular-nums">{{ fmtMoney(payBillRow.paid_amount) }} 元</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">欠缴额</span><strong class="text-rose-500 tabular-nums">{{ fmtMoney(arrearsOf(payBillRow)) }} 元</strong></div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">本次收款（元）*</label>
          <input v-model.number="payAmount" type="number" step="0.01" min="0.01" class="input-field" required />
          <div class="flex gap-2 mt-2">
            <button type="button" class="btn-ghost !py-1 !px-3 text-xs" @click="payAmount = arrearsOf(payBillRow)">补齐全部欠缴</button>
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <input v-model="payNote" class="input-field" placeholder="选填，如现金/转账" />
        </div>
        <p v-if="payError" class="text-sm text-destructive">{{ payError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showPay = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '登记中…' : '确认收款' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 单据 -->
    <Modal v-model="showReceipt" title="收费单据" >
      <div v-if="receiptBill">
        <RentalReceipt :bill="receiptBill" :property="receiptProperty" />
        <div class="flex justify-end gap-3 mt-4 print:hidden">
          <button class="btn-ghost" @click="showReceipt = false">关闭</button>
          <button class="btn-brand" @click="printReceipt">🖨 打印 / 另存 PDF</button>
        </div>
      </div>
    </Modal>

    <!-- 导入 -->
    <Modal v-model="showImport" title="导入账单（CSV 模板）">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground leading-relaxed">
          请使用系统提供的模板填写后上传（.csv，≤2MB）。房号不存在将自动建房；同房同月已有账单将更新。
        </p>
        <input ref="fileInput" type="file" accept=".csv" class="input-field !py-2" />
        <div v-if="importResult" class="rounded-xl border border-border p-4 text-sm space-y-2">
          <div class="flex gap-4">
            <span class="text-emerald-600 dark:text-emerald-400">新建 {{ importResult.created }}</span>
            <span class="text-brand-600 dark:text-brand-300">更新 {{ importResult.updated }}</span>
            <span class="text-destructive">失败 {{ importResult.failed }}</span>
          </div>
          <ul v-if="importResult.errors.length" class="text-xs text-destructive space-y-1 max-h-32 overflow-auto">
            <li v-for="(e, i) in importResult.errors" :key="i">第 {{ e.line }} 行：{{ e.reason }}</li>
          </ul>
        </div>
        <p v-if="importError" class="text-sm text-destructive">{{ importError }}</p>
        <div class="flex gap-3">
          <button class="btn-ghost flex-1" @click="showImport = false">关闭</button>
          <button class="btn-brand flex-1" :disabled="importing" @click="doImport">{{ importing ? '导入中…' : '开始导入' }}</button>
        </div>
      </div>
    </Modal>

    <!-- 单间建账入口已并入「抄表录入」：选房间抄表保存即建账 -->

    <ConfirmDialog
      v-model="confirmState.show"
      :title="confirmState.title"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="confirmState.action?.()"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import RentalReceipt from '../components/RentalReceipt.vue'
import { toast } from '../utils/toast'
import {
  arrearsOf, createBill, currentPeriod, deleteBill, downloadTemplate, exportBills, fmtMoney,
  getBillDetail, getBills, getRooms, importBills, isArrears, payBill, updateBill,
  type Bill, type ImportResult, type PropertyMeta, type RoomView,
} from '../api/rental'

const bills = ref<Bill[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const period = ref(currentPeriod())
const status = ref('')
const keyword = ref('')
const saving = ref(false)

const showEdit = ref(false)
const editForm = ref<Bill | null>(null)
const editError = ref('')

const showPay = ref(false)
const payBillRow = ref<Bill | null>(null)
const payAmount = ref(0)
const payNote = ref('')
const payError = ref('')

const showReceipt = ref(false)
const receiptBill = ref<Bill | null>(null)
const receiptProperty = ref<PropertyMeta>({ name: '', contact: '', note: '' })

const showImport = ref(false)
const importing = ref(false)
const importError = ref('')
const importResult = ref<ImportResult | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const pageSum = computed(() => {
  let t = 0
  let a = 0
  for (const b of bills.value) {
    t += b.total_amount
    a += arrearsOf(b)
  }
  return { total: t, arrears: a }
})

const meterBlocks = [
  { label: '水费', lastKey: 'water_last', nowKey: 'water_now', priceKey: 'water_price' },
  { label: '电费', lastKey: 'elec_last', nowKey: 'elec_now', priceKey: 'elec_price' },
  { label: '燃气费', lastKey: 'gas_last', nowKey: 'gas_now', priceKey: 'gas_price' },
] as const

function previewFee(last: number, now: number, price: number): number {
  const usage = now > last ? now - last : 0
  return Math.round(usage * price * 100) / 100
}

function previewTotal(): number {
  const f = editForm.value
  if (!f) return 0
  const t =
    (f.rent || 0) +
    previewFee(f.water_last, f.water_now, f.water_price) +
    previewFee(f.elec_last, f.elec_now, f.elec_price) +
    previewFee(f.gas_last, f.gas_now, f.gas_price) +
    (f.sanitation_fee || 0) +
    (f.management_fee || 0)
  return Math.round(t * 100) / 100
}

function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

function statusText(b: Bill) {
  if (b.status === 'paid') return '已缴清'
  if (b.status === 'partial') return '部分已缴'
  return '未缴纳'
}
function statusClass(b: Bill) {
  if (b.status === 'paid') return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  if (b.status === 'partial') return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getBills({
      page: p, pageSize,
      period: period.value, status: status.value, keyword: keyword.value,
    })
    if (res.data?.code === 0) {
      bills.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function loadRooms() {
  const res = await getRooms({ page: 1, pageSize: 100 })
  if (res.data?.code === 0) allRooms.value = res.data.data.items || []
}

function doExport() {
  exportBills(period.value)
}

const confirmState = ref({ show: false, title: '', message: '', action: null as null | (() => Promise<void>) })

// 抄表录入：只录本月读数，上月读数与单价自动带入，费用实时预估
// 抄表录入是独立表单：账期自选、候选是全部在租房间（不管该月有没有账单）。
// 已有账单 → 带出可编辑；没有账单 → 预览态（房间默认值 + 上月读数衔接），
// 保存时自动建账——先抄表、账单由读数算出来，不要求先建账。
const showReading = ref(false)
const readingPeriod = ref(currentPeriod())
const readingBills = ref<Bill[]>([])
const readingPrevBills = ref<Bill[]>([])
const readingRooms = ref<RoomView[]>([])
const readingLoading = ref(false)
const readingBillId = ref(0) // >0：已有账单的 id
const readingRoomId = ref(0) // 无账单时选中的房间 id
const readingForm = ref<Bill | null>(null)
const readingIsNew = ref(false) // true = 该房间该月还没有账单，保存走建账
const readingError = ref('')

// 上月账期：YYYY-MM → 上个月
function prevPeriod(period: string): string {
  const [y, m] = period.split('-').map(Number)
  if (!y || !m) return period
  const d = new Date(y, m - 2, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

// 抄表候选：全部在租房间。每间标注该账期是否已有账单及对应账单 id。
interface ReadingCandidate {
  room: RoomView
  bill: Bill | null
}

const readingCandidates = computed<ReadingCandidate[]>(() => {
  const byRoom = new Map<number, Bill>()
  for (const b of readingBills.value) byRoom.set(b.room_id, b)
  return readingRooms.value.map((room) => ({ room, bill: byRoom.get(room.id) ?? null }))
})

// 该账期"已有账单的房间数"，用于空态提示（一个账单都没有才提示先建账/直接抄表均可）
const readingBillCount = computed(() => readingBills.value.length)

async function loadReadingData() {
  readingLoading.value = true
  readingError.value = ''
  try {
    // 同时取本账期与上一账期的账单：后者用于未建账房间预览"上月读数"（与后端 prefillReadings 同口径）
    const [billsRes, prevRes, roomsRes] = await Promise.all([
      getBills({ page: 1, pageSize: 200, period: readingPeriod.value }),
      getBills({ page: 1, pageSize: 200, period: prevPeriod(readingPeriod.value) }),
      getRooms({ page: 1, pageSize: 200, status: 'occupied' }),
    ])
    readingBills.value = billsRes.data?.code === 0 ? billsRes.data.data.items || [] : []
    readingPrevBills.value = prevRes.data?.code === 0 ? prevRes.data.data.items || [] : []
    readingRooms.value = roomsRes.data?.code === 0 ? roomsRes.data.data.items || [] : []
  } catch {
    readingBills.value = []
    readingPrevBills.value = []
    readingRooms.value = []
    readingError.value = '加载失败，请重试'
  } finally {
    readingLoading.value = false
  }
}

// 选中某间房间：有账单带出账单；无账单构造预览行（本月读数留空待填，
// 上月读数取上一期账单的本月读数，无历史则房间底数——与后端 prefillReadings 同口径）
function selectReadingCandidate(c: ReadingCandidate | null) {
  if (!c) {
    readingForm.value = null
    readingIsNew.value = false
    return
  }
  if (c.bill) {
    readingBillId.value = c.bill.id
    readingRoomId.value = 0
    readingForm.value = { ...c.bill }
    readingIsNew.value = false
  } else {
    const room = c.room
    const prev = readingPrevBills.value.find((b) => b.room_id === room.id)
    const waterLast = prev?.water_now ?? room.initial_water ?? 0
    const elecLast = prev?.elec_now ?? room.initial_elec ?? 0
    const gasLast = prev?.gas_now ?? room.initial_gas ?? 0
    readingBillId.value = 0
    readingRoomId.value = room.id
    readingForm.value = {
      id: 0, room_id: room.id, period: readingPeriod.value, room_no: room.room_no,
      tenant_name: room.current_tenants?.map((t) => t.name).join('、') || '—',
      rent: room.default_rent, water_last: waterLast, water_now: waterLast,
      elec_last: elecLast, elec_now: elecLast, gas_last: gasLast, gas_now: gasLast,
      water_price: room.water_price, elec_price: room.elec_price, gas_price: room.gas_price,
      sanitation_fee: room.default_sanitation_fee, management_fee: room.default_management_fee,
    } as Bill
    readingIsNew.value = true
  }
}

function openReading(b?: Bill) {
  readingError.value = ''
  showReading.value = true
  if (b) {
    readingPeriod.value = b.period
    loadReadingData().then(() => {
      const c = readingCandidates.value.find((x) => x.room.id === b.room_id) ?? null
      selectReadingCandidate(c)
    })
  } else {
    readingPeriod.value = currentPeriod()
    loadReadingData().then(() => {
      // 默认选第一间：优先已有账单的房间
      const first = readingCandidates.value.find((x) => x.bill) ?? readingCandidates.value[0] ?? null
      selectReadingCandidate(first)
    })
  }
}

function pickReadingRoom() {
  const c = readingCandidates.value.find((x) => (x.bill ? x.bill.id === readingBillId.value : x.room.id === readingRoomId.value))
  selectReadingCandidate(c ?? null)
  readingError.value = ''
}

// 下拉按 id 分发：先按账单 id 找（已建账），再按房间 id 找（未建账）
function onReadingPick(e: Event) {
  const v = Number((e.target as HTMLSelectElement).value)
  const c = readingCandidates.value.find((x) => (x.bill ? x.bill.id === v : x.room.id === v))
  selectReadingCandidate(c ?? null)
  readingError.value = ''
}

function readingTotal(): number {
  const f = readingForm.value
  if (!f) return 0
  const t =
    (f.rent || 0) +
    previewFee(f.water_last, f.water_now, f.water_price) +
    previewFee(f.elec_last, f.elec_now, f.elec_price) +
    previewFee(f.gas_last, f.gas_now, f.gas_price) +
    (f.sanitation_fee || 0) +
    (f.management_fee || 0)
  return Math.round(t * 100) / 100
}

async function saveReading() {
  const f = readingForm.value
  if (!f) return
  if ((f.water_now ?? 0) < (f.water_last ?? 0) || (f.elec_now ?? 0) < (f.elec_last ?? 0) || (f.gas_now ?? 0) < (f.gas_last ?? 0)) {
    readingError.value = '本月读数不能小于上月读数，请核对后再保存'
    return
  }
  saving.value = true
  readingError.value = ''
  try {
    // 无账单：带读数直建账单；有账单：更新读数
    const res = readingIsNew.value
      ? await createBill(f.room_id, f.period, {
          water_now: f.water_now, elec_now: f.elec_now, gas_now: f.gas_now,
          water_price: f.water_price, elec_price: f.elec_price, gas_price: f.gas_price, rent: f.rent,
        })
      : await updateBill(f.id, f)
    if (res.data?.code === 0) {
      toast(`已保存 ${f.room_no} ${f.period} 抄表读数${readingIsNew.value ? '（已生成账单）' : ''}`)
      await load(page.value)
      // 弹窗内连续抄表：刷新该账期名单（当前间标记已抄），自动跳到下一间未抄的；全部抄完才收弹窗
      await loadReadingData()
      const next = readingCandidates.value.find((x) => !x.bill)
      if (next) {
        selectReadingCandidate(next)
      } else {
        showReading.value = false
        toast(`${readingPeriod.value} 全部房间已抄完`)
      }
    } else {
      readingError.value = res.data?.message || '保存失败'
    }
  } catch {
    readingError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

async function openEdit(b: Bill) {
  editForm.value = { ...b }
  editError.value = ''
  showEdit.value = true
}

async function saveEdit() {
  const f = editForm.value
  if (!f) return
  if (!/^\d{4}-\d{2}$/.test(f.period)) {
    editError.value = '账期格式应为 YYYY-MM'
    return
  }
  saving.value = true
  editError.value = ''
  try {
    const res = await updateBill(f.id, f)
    if (res.data?.code === 0) {
      showEdit.value = false
      await load(page.value)
    } else {
      editError.value = res.data?.message || '保存失败'
    }
  } catch {
    editError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

function openPay(b: Bill) {
  payBillRow.value = { ...b }
  payAmount.value = arrearsOf(b)
  payNote.value = ''
  payError.value = ''
  showPay.value = true
}

async function savePay() {
  const row = payBillRow.value
  if (!row || payAmount.value <= 0) {
    payError.value = '收款金额必须大于 0'
    return
  }
  saving.value = true
  payError.value = ''
  try {
    const res = await payBill(row.id, payAmount.value, payNote.value)
    if (res.data?.code === 0) {
      showPay.value = false
      await load(page.value)
    } else {
      payError.value = res.data?.message || '登记失败'
    }
  } catch {
    payError.value = '登记失败，请重试'
  } finally {
    saving.value = false
  }
}

async function openReceipt(b: Bill) {
  const res = await getBillDetail(b.id)
  if (res.data?.code === 0) {
    receiptBill.value = res.data.data.bill
    receiptProperty.value = res.data.data.property
    showReceipt.value = true
  }
}

function printReceipt() {
  window.print()
}

async function doImport() {
  const file = fileInput.value?.files?.[0]
  if (!file) {
    importError.value = '请先选择 CSV 文件'
    return
  }
  importing.value = true
  importError.value = ''
  importResult.value = null
  try {
    const res = await importBills(file)
    const env = res.data as unknown as { code: number; message: string; data?: ImportResult }
    if (env?.code === 0) {
      importResult.value = env.data ?? { created: 0, updated: 0, failed: 0, errors: [] }
      await load(1)
    } else {
      importError.value = env?.message || '导入失败'
    }
  } catch {
    importError.value = '导入失败，请检查文件格式'
  } finally {
    importing.value = false
  }
}

function remove(b: Bill) {
  confirmState.value = {
    show: true,
    title: '删除账单',
    message: `确定删除 ${b.period} ${b.room_no} 的账单？该操作不可恢复。`,
    action: async () => {
      const res = await deleteBill(b.id)
      if (res.data?.code !== 0) {
        toast(res.data?.message || '删除失败', 'error')
        return
      }
      toast('已删除')
      await load(page.value)
    },
  }
}

onMounted(async () => {
  await loadRooms()
  await load(1)
})
</script>
