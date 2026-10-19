<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="抄表账单" description="月度抄表、费用自动计算、收款与欠缴跟踪、收费单据">
      <template #actions>
        <button class="btn-ghost" @click="downloadTemplate">下载模板</button>
        <button class="btn-ghost" @click="doExport">导出</button>
        <button class="btn-ghost" @click="openBatchPrint">打印单据</button>
        <button v-if="ledgerStore.canFull('bills')" class="btn-ghost" @click="showImport = true">导入</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="billsFilterSummary" :active-count="billsFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <DateField v-model="periodFrom" type="month" class="!py-2 !w-[140px]" placeholder="账期开始" @change="load(1)" />
        <span class="text-xs text-muted-foreground shrink-0">至</span>
        <DateField v-model="periodTo" type="month" class="!py-2 !w-[140px]" placeholder="账期结束" @change="load(1)" />
        <select v-model="billType" class="input-field !py-2 !w-auto" @change="load(1)">
          <option value="">全部类型</option>
          <!-- 账单类型 = 开票时快照的缴费周期（后端 bill_type 筛选接受四个周期值）；
               选项与文案都从 utils/cycle.ts 取，避免再写死「月账单/季度账单」两项 -->
          <option v-for="o in payCycleOptions" :key="o.value" :value="o.value">{{ billTypeLabel(o.value) }}</option>
        </select>
        <select v-model="status" class="input-field !py-2 !w-auto" @change="load(1)">
          <option value="">全部状态</option>
          <option value="arrears">欠缴（含部分）</option>
          <option value="unpaid">未缴纳</option>
          <option value="partial">部分已缴</option>
          <option value="paid">已缴清</option>
        </select>
        <select v-model="filterCommunity" class="input-field !py-2 !w-auto max-w-[150px]" @change="onCommunityChange">
          <option value="">全部小区</option>
          <option v-for="c in communityOptions" :key="c" :value="c">{{ c }}</option>
        </select>
        <select v-model="filterRoomId" class="input-field !py-2 !w-auto max-w-[150px]" @change="load(1)">
          <option :value="0">全部房源</option>
          <option v-for="r in roomOptions" :key="r.id" :value="r.id">{{ r.room_no }}</option>
        </select>
        <input v-model="keyword" class="input-field flex-1 min-w-[160px] !py-2" placeholder="搜索房号或租户…" @keyup.enter="applyFilters" />
        <button class="btn-brand !py-2" @click="applyFilters">查询</button>
        <div class="sm:ml-auto text-sm text-muted-foreground whitespace-nowrap w-full sm:w-auto">
          本页合计：应收 <strong class="text-foreground tabular-nums">{{ fmtMoney(pageSum.total) }}</strong> 元 ·
          欠缴 <strong class="text-rose-500 tabular-nums">{{ fmtMoney(pageSum.arrears) }}</strong> 元
        </div>
      </div>
    </FilterPanel>

    <!-- 账单表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="bills.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        暂无账单。到「抄表」模块抄表开票（读数保存后账单自动生成），或点右上角「导入」批量建账。
        <div class="mt-3"><RouterLink to="/admin/meters" class="btn-ghost !py-1.5 inline-flex items-center">⚡ 去抄表开票</RouterLink></div>
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
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">卫生费（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">管理费（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom" title="入住押金：只在租户首张账单上收一次">押金（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">本月租金（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">已收（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">欠缴额（元）</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium align-bottom">状态</th>
              <th rowspan="2" class="py-2.5 px-3 font-medium text-right align-bottom">操作</th>
            </tr>
            <tr class="text-left text-muted-foreground/80 text-xs border-b border-border">
              <th class="py-1.5 px-3 font-medium text-right">水表（吨）</th>
              <th class="py-1.5 px-3 font-medium text-right">电表（度）</th>
              <th class="py-1.5 px-3 font-medium text-right">燃气表（方）</th>
              <th class="py-1.5 px-3 font-medium text-right">水费（元）</th>
              <th class="py-1.5 px-3 font-medium text-right">电费（元）</th>
              <th class="py-1.5 px-3 font-medium text-right">燃气费（元）</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="b in bills" :key="b.id" class="hover:bg-muted/30 transition-colors" :class="isArrears(b) ? 'bg-rose-500/[0.03]' : ''">
              <td class="py-2.5 px-3 text-muted-foreground tabular-nums whitespace-nowrap">
                <span :title="coveredPeriodText(b.period, b.pay_cycle)">{{ b.period }}</span>
                <span v-if="cycleMonths(b.pay_cycle) > 1" class="badge shrink-0 ml-1 !px-1.5 !py-0.5 text-[11px] bg-indigo-500/10 text-indigo-600 dark:text-indigo-300" :title="cycleBadgeTitle(b.pay_cycle, b.period)">{{ cycleLabel(b.pay_cycle) }}</span>
              </td>
              <td class="py-2.5 px-3 font-semibold text-foreground whitespace-nowrap">{{ b.room_no }}</td>
              <td class="py-2.5 px-3 text-foreground whitespace-nowrap max-w-[120px] truncate" :title="b.tenant_name">{{ b.tenant_name || '—' }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ waterReadingText(b) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ meterReadingText(b, 'elec') }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums text-muted-foreground whitespace-nowrap">{{ meterReadingText(b, 'gas') }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.water_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.elec_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.gas_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.sanitation_fee) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(b.management_fee) }}</td>
              <!-- 押金：只在首张账单有值，其余账期显示 —（避免一列全是 0.00 看着乱） -->
              <td class="py-2.5 px-3 text-right tabular-nums">
                <span v-if="b.deposit_fee" class="text-amber-600 dark:text-amber-300 font-semibold">{{ fmtMoney(b.deposit_fee) }}</span>
                <span v-else class="text-muted-foreground">—</span>
              </td>
              <!-- 应缴 = 本期费用 + 上期结转。开了结转的台账里房东关心的是
                   「这户该交多少」，所以这一列显示 payable；有结转时把差额标出来，
                   否则用户会觉得「本期各项加起来怎么对不上这个数」。 -->
              <td class="py-2.5 px-3 text-right tabular-nums font-bold text-foreground">
                {{ fmtMoney(payableOf(b)) }}
                <div v-if="carryInOf(b)" class="text-[10px] font-normal text-amber-600 dark:text-amber-300 whitespace-nowrap">
                  含上期结转 {{ carryInOf(b) > 0 ? '+' : '' }}{{ fmtMoney(carryInOf(b)) }}
                </div>
              </td>
              <td class="py-2.5 px-3 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(b.paid_amount) }}</td>
              <td class="py-2.5 px-3 text-right tabular-nums" :class="isArrears(b) ? 'text-rose-500 font-semibold' : ''">{{ fmtMoney(arrearsOf(b)) }}</td>
              <td class="py-2.5 px-3">
                <span class="badge" :class="statusClass(b)">{{ statusText(b) }}</span>
              </td>
              <td class="py-2.5 px-3 text-right whitespace-nowrap">
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-2.5" @click="openDetail(b)">详情</button>
                <button v-if="ledgerStore.canEdit('bills')" class="text-brand-600 dark:text-brand-300 hover:underline mr-2.5" @click="openReading(b)">抄表</button>
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-2.5" @click="openReceipt(b)">单据</button>
                <button v-if="ledgerStore.canEdit('bills')" class="text-foreground hover:underline mr-2.5" @click="openEdit(b)">编辑</button>
                <button v-if="ledgerStore.canEdit('bills') && isArrears(b)" class="text-emerald-600 dark:text-emerald-400 hover:underline mr-2.5" @click="openPay(b)">收款</button>
                <button v-if="ledgerStore.canFull('bills')" class="text-destructive hover:underline" @click="remove(b)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表（紧凑版） -->
      <div class="md:hidden p-2 space-y-1.5">
        <div v-for="b in bills" :key="b.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5" :class="isArrears(b) ? 'bg-rose-500/[0.04]' : ''">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <div class="flex items-center gap-1.5">
                <span class="text-sm font-semibold text-foreground">{{ b.room_no }}</span>
                <span class="text-[11px] text-muted-foreground tabular-nums">{{ coveredPeriodText(b.period, b.pay_cycle) }}</span>
                <span v-if="cycleMonths(b.pay_cycle) > 1" class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-indigo-500/10 text-indigo-600 dark:text-indigo-300" :title="cycleBadgeTitle(b.pay_cycle, b.period)">{{ cycleLabel(b.pay_cycle) }}</span>
                <span class="text-[11px] text-muted-foreground truncate">{{ b.tenant_name || '—' }}</span>
              </div>
            </div>
            <span class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]" :class="statusClass(b)">{{ statusText(b) }}</span>
          </div>

          <div class="text-[11px] text-muted-foreground tabular-nums rounded-md bg-muted/50 px-2 py-1.5">
            <div>水 {{ waterReadingText(b, '→') }} 电 {{ meterReadingText(b, 'elec') }} 气 {{ meterReadingText(b, 'gas') }}</div>
            <div class="mt-0.5">水费 {{ fmtMoney(b.water_fee) }} 元 · 电费 {{ fmtMoney(b.elec_fee) }} 元 · 燃气 {{ fmtMoney(b.gas_fee) }} 元 · 卫生 {{ fmtMoney(b.sanitation_fee) }} 元 · 管理 {{ fmtMoney(b.management_fee) }} 元</div>
            <!-- 押金只在首张账单出现；手机端单独一行并标黄，避免和普通费用混在一起 -->
            <div v-if="b.deposit_fee" class="mt-0.5 text-amber-600 dark:text-amber-300 font-medium">押金 {{ fmtMoney(b.deposit_fee) }} 元（入住押金，退租时结算退还）</div>
          </div>

          <div class="flex items-end justify-between gap-2">
            <div class="text-[11px] text-muted-foreground">
              应付合计 <span class="text-sm font-bold text-foreground tabular-nums">{{ fmtMoney(payableOf(b)) }}</span> 元
              <div v-if="carryInOf(b)" class="text-[10px] text-amber-600 dark:text-amber-300 tabular-nums">
                含上期结转 {{ carryInOf(b) > 0 ? '+' : '' }}{{ fmtMoney(carryInOf(b)) }} 元
              </div>
            </div>
            <div class="text-[11px] text-right space-y-0">
              <div class="text-emerald-600 dark:text-emerald-400 tabular-nums">已收 {{ fmtMoney(b.paid_amount) }} 元</div>
              <div class="tabular-nums" :class="isArrears(b) ? 'text-rose-500 font-semibold' : 'text-muted-foreground'">欠缴 {{ fmtMoney(arrearsOf(b)) }} 元</div>
            </div>
          </div>

          <div class="flex items-center gap-4 pt-1.5 border-t border-border/60 text-xs">
            <button class="text-brand-600 dark:text-brand-300" @click="openDetail(b)">详情</button>
            <button v-if="ledgerStore.canEdit('bills')" class="text-brand-600 dark:text-brand-300" @click="openReading(b)">抄表</button>
            <button class="text-brand-600 dark:text-brand-300" @click="openReceipt(b)">单据</button>
            <button v-if="ledgerStore.canEdit('bills')" class="text-foreground" @click="openEdit(b)">编辑</button>
            <button v-if="ledgerStore.canEdit('bills') && isArrears(b)" class="text-emerald-600 dark:text-emerald-400" @click="openPay(b)">收款</button>
            <button v-if="ledgerStore.canFull('bills')" class="text-destructive ml-auto" @click="remove(b)">删除</button>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 编辑账单 -->
    <Modal v-model="showEdit" title="编辑账单" >
      <form v-if="editForm" class="space-y-4" @submit.prevent="saveEdit">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">账期</label>
            <div class="flex items-center gap-2">
              <input v-model="editForm.period" class="input-field tabular-nums flex-1 min-w-0" placeholder="2026-09" />
              <span
                v-if="cycleMonths(editForm.pay_cycle) > 1"
                class="badge shrink-0 bg-indigo-500/10 text-indigo-600 dark:text-indigo-300"
                :title="`${cycleLabel(editForm.pay_cycle)}账单覆盖 ${cycleMonths(editForm.pay_cycle)} 个月，租金与包月费用按 ${cycleMonths(editForm.pay_cycle)} 个月计`"
              >
                {{ cycleLabel(editForm.pay_cycle) }} · {{ coveredPeriodText(editForm.period, editForm.pay_cycle) }}
              </span>
            </div>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">租户名</label>
            <input v-model="editForm.tenant_name" class="input-field" />
          </div>
        </div>
        <p v-if="editExcludedNames" class="text-[11px] text-muted-foreground bg-muted/60 rounded-lg px-2.5 py-2">
          本租户不参与：{{ editExcludedNames }}。这些项目按 0 结算，相关输入已停用；如需调整请到「租户管理」修改收费项目勾选（历史账单不受影响）。
          <template v-if="editDisabledNames">
            <br />台账已停用：{{ editDisabledNames }}（在「账单 → 收费项目」里可重新启用）。
          </template>
        </p>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">租金（元）</label>
          <input v-model.number="editForm.rent" type="number" step="0.01" min="0" class="input-field" :disabled="feeExcluded('rent')" />
        </div>
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <p class="text-xs font-semibold text-muted-foreground">抄表（上月 / 本月读数与单价）</p>
          <!-- 水费单独成块：可切换按吨（抄表）/ 包月（固定金额），包月不看读数 -->
          <div v-if="editForm" class="rounded-lg bg-muted/60 p-2.5">
            <div class="flex items-center justify-between mb-1.5 gap-2">
              <span class="text-sm font-medium text-foreground flex items-center gap-2">
                水费
                <select v-if="!feeExcluded('water')" v-model="editForm.water_mode" class="input-field !py-1 !px-2 !w-auto text-xs" @change="onEditWaterModeChange">
                  <option value="meter">按吨计价</option>
                  <option value="monthly">包月</option>
                </select>
              </span>
              <span class="text-xs tabular-nums text-muted-foreground">
                费用预估 <strong class="text-foreground">{{ fmtMoney(feeExcluded('water') ? 0 : waterPreview(editForm)) }}</strong> 元
              </span>
            </div>
            <p v-if="feeExcluded('water')" class="text-[11px] text-muted-foreground">
              {{ feeDisabledByLedger('water') ? '「水费」已在收费项目里停用' : '本租户不参与水费收费' }}，出账费用为 0（读数仍会记录）。
            </p>
            <template v-else>
            <p v-if="editRoomBilling" class="text-[11px] text-muted-foreground mb-1.5">
              该房按租户设置：{{ editRoomBilling }}（切换计费方式会按对应口径的配置金额填入，仍可手改）
            </p>
            <label v-if="editForm.water_mode === 'monthly'" class="block">
              <span class="text-[11px] text-muted-foreground">包月金额（元/月）</span>
              <input v-model.number="editForm.water_price" type="number" step="0.01" min="0" class="input-field !px-2 !py-1.5 text-right" />
            </label>
            <div v-else class="grid grid-cols-2 sm:grid-cols-3 gap-2">
              <label class="block">
                <span class="text-[11px] text-muted-foreground">上月读数（吨）</span>
                <input v-model.number="editForm.water_last" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">本月读数（吨）</span>
                <input v-model.number="editForm.water_now" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/吨）</span>
                <input v-model.number="editForm.water_price" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
            </div>
            </template>
          </div>
          <div v-for="m in meterBlocks" :key="m.lastKey" class="rounded-lg bg-muted/60 p-2.5">
            <div class="flex items-center justify-between mb-1.5">
              <span class="text-sm font-medium text-foreground">{{ m.label }}</span>
              <span class="text-xs tabular-nums text-muted-foreground">
                费用预估 <strong class="text-foreground">{{ fmtMoney(feeExcluded(m.feeKey) ? 0 : previewFee(editForm[m.lastKey], editForm[m.nowKey], editForm[m.priceKey])) }}</strong> 元
              </span>
            </div>
            <p v-if="feeExcluded(m.feeKey)" class="text-[11px] text-muted-foreground">
              {{ feeDisabledByLedger(m.feeKey) ? `「${m.label}」已在收费项目里停用` : `本租户不参与${m.label}收费` }}，出账费用为 0（读数仍会记录）。
            </p>
            <div v-else class="grid grid-cols-2 sm:grid-cols-3 gap-2">
              <label class="block">
                <span class="text-[11px] text-muted-foreground">上月读数（{{ m.unit }}）</span>
                <input v-model.number="editForm[m.lastKey]" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">本月读数（{{ m.unit }}）</span>
                <input v-model.number="editForm[m.nowKey]" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/{{ m.unit }}）</span>
                <input v-model.number="editForm[m.priceKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
            </div>
          </div>
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">卫生费（元）</label>
            <input v-model.number="editForm.sanitation_fee" type="number" step="0.01" min="0" class="input-field" :disabled="feeExcluded('sanitation')" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">管理费（元）</label>
            <input v-model.number="editForm.management_fee" type="number" step="0.01" min="0" class="input-field" :disabled="feeExcluded('management')" />
          </div>
          <!-- 押金：只在首张账单上出现。不参与「排除项」（服务端也不受它影响），
               这里始终可编辑，方便手动把押金改掉或清零。 -->
          <!-- 输入框必须始终渲染：此前写成 v-if="editForm.deposit_fee"，一旦押金改成
               0（或账单本来就没押金）输入框整个消失，再也补不回去，与上面「始终可编辑」
               的注释自相矛盾（2026-10-11 审计）。 -->
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">
              押金（元）
              <span class="font-normal text-[11px]">· 入住押金，只收一次，0 表示不收</span>
            </label>
            <input v-model.number="editForm.deposit_fee" type="number" step="0.01" min="0" class="input-field" />
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

    <!-- 抄表开票弹窗：与抄表记录页共用（入口已迁至「抄表」模块，这里保留单间读数编辑） -->
    <MeterReadingModal v-model="showReading" :initial-bill="readingTarget" @saved="load(page)" />

    <!-- 收款：按项目缴（本期收哪些项目、各缴多少），生成收款流水 -->
    <Modal v-model="showPay" title="登记收款">
      <form v-if="payBillRow" class="space-y-4" @submit.prevent="savePay">
        <div class="rounded-xl bg-muted p-4 text-sm space-y-1.5">
          <div class="flex justify-between"><span class="text-muted-foreground">房号 / 租户</span><span class="font-semibold text-foreground">{{ payBillRow.room_no }} · {{ payBillRow.tenant_name || '—' }}</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">应付合计</span><span class="tabular-nums">{{ fmtMoney(payBillRow.total_amount) }} 元</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">已收</span><span class="tabular-nums">{{ fmtMoney(payBillRow.paid_amount) }} 元</span></div>
          <div class="flex justify-between"><span class="text-muted-foreground">欠缴额</span><strong class="text-rose-500 tabular-nums">{{ fmtMoney(arrearsOf(payBillRow)) }} 元</strong></div>
        </div>

        <div>
          <div class="flex items-center justify-between mb-1.5">
            <span class="text-xs font-semibold text-muted-foreground">按项目收款（勾选本期收的项目，金额可改）</span>
            <button type="button" class="text-xs text-brand-600 dark:text-brand-300" @click="fillAllArrears">补齐全部欠缴</button>
          </div>
          <div v-if="payItems.length" class="rounded-xl border border-border divide-y divide-border/60">
            <label v-for="it in payItems" :key="it.key" class="flex items-center gap-2.5 px-3 py-2 cursor-pointer">
              <input v-model="it.checked" type="checkbox" class="w-4 h-4 accent-indigo-500 shrink-0" @change="onPayItemCheck(it)" />
              <div class="min-w-0 flex-1">
                <div class="text-sm font-medium text-foreground truncate">{{ it.name }}</div>
                <div class="text-[11px] text-muted-foreground tabular-nums">
                  应付 {{ fmtMoney(it.amount) }}<template v-if="it.paid > 0"> · 已收 {{ fmtMoney(it.paid) }}</template><template v-if="it.detail"> · {{ it.detail }}</template>
                </div>
              </div>
              <input
                v-if="it.checked"
                v-model.number="it.payAmount" type="number" step="0.01" min="0.01"
                class="input-field !py-1.5 !px-2 text-right w-[104px] shrink-0"
              />
              <span v-else class="text-[11px] tabular-nums shrink-0" :class="it.arrears > 0.005 ? 'text-rose-500' : 'text-emerald-600 dark:text-emerald-400'">
                {{ it.arrears > 0.005 ? `欠 ${fmtMoney(it.arrears)}` : '已清' }}
              </span>
            </label>
          </div>
          <p v-else class="text-xs text-muted-foreground bg-muted/60 rounded-lg px-3 py-2">
            该账单还没有费用明细（历史账单）。可直接填总额收款。
          </p>
        </div>

        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">本次收款合计（元）*</label>
          <input v-model.number="payAmount" type="number" step="0.01" min="0.01" class="input-field" required />
          <p v-if="payItems.length" class="text-[11px] text-muted-foreground mt-1">勾选项目的金额合计应等于本次收款合计；收款会记入收款记录。</p>
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

    <!-- 批量导入（v1.0.0 引入，重构中模板块丢失导致按钮点开无弹窗，现恢复） -->
    <Modal v-model="showImport" title="导入账单（CSV 模板）">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground leading-relaxed">
          请使用系统提供的模板填写后上传（.csv，≤2MB）。房号不存在将自动建房；同房同月已有账单将更新。
        </p>
        <FilePick
          accept=".csv"
          :disabled="importing"
          :label="importFileName || '点击选择 CSV 文件'"
          hint="仅支持 .csv（Excel/WPS 编辑后另存为 CSV），≤2MB"
          @change="onPickImportFile"
        />
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

    <!-- 单据：wide —— 单据是六列明细表，默认 512px 的弹窗会把最右侧的
         「金额 / 缴费状态」裁掉（issue #3 现象①），这里放宽到 4xl。 -->
    <Modal v-model="showReceipt" title="收费单据" wide>
      <div v-if="receiptBill">
        <RentalReceipt :bill="receiptBill" :property="receiptProperty" :items="receiptItems" />
        <div class="flex flex-wrap items-center justify-end gap-3 mt-4 print:hidden">
          <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            <span>单据纸张</span>
            <ReceiptPaperPicker v-model:paper="receiptPaper" v-model:custom="customPaper" @update:paper="onPaperChange" @update:custom="onPaperChange" />
          </div>
          <button class="btn-ghost" @click="showReceipt = false">关闭</button>
          <button class="btn-brand" @click="printReceipt">🖨 打印 / 另存 PDF</button>
        </div>
      </div>
    </Modal>

    <!-- 批量打印单据：选账期区间 → 预览数量 → 逐张分页打印 -->
    <Modal v-model="showBatchPrint" title="批量打印收费单据">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground leading-relaxed">
          按账期区间批量打印收费单据（每张一页，区间两端可留空表示不限）。导出数据用右上角「导出」。
        </p>
        <div class="flex flex-wrap items-center gap-2">
          <DateField v-model="batchFrom" type="month" class="!py-2 !w-[140px]" placeholder="起始账期" />
          <span class="text-xs text-muted-foreground shrink-0">至</span>
          <DateField v-model="batchTo" type="month" class="!py-2 !w-[140px]" placeholder="截止账期" />
          <button class="btn-brand !py-2" :disabled="batchLoading" @click="previewReceipts">{{ batchLoading ? '查询中…' : '查询预览' }}</button>
        </div>
        <p v-if="batchError" class="text-sm text-destructive">{{ batchError }}</p>
        <div v-if="batchQueried" class="rounded-xl bg-muted/60 px-3 py-2.5 text-sm text-foreground">
          共 <strong class="tabular-nums">{{ batchBills.length }}</strong> 张单据
          <span v-if="batchBills.length >= 500" class="text-amber-600 dark:text-amber-300 text-xs ml-1">已达单批上限 500 张，请缩小区间分批打印</span>
          <span v-else-if="batchBills.length === 0" class="text-muted-foreground text-xs ml-1">区间内没有账单</span>
        </div>
        <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <span>单据纸张</span>
          <ReceiptPaperPicker v-model:paper="receiptPaper" v-model:custom="customPaper" @update:paper="onPaperChange" @update:custom="onPaperChange" />
          <span class="text-[11px]">（设置保存在本机）</span>
        </div>
        <div class="flex gap-3">
          <button class="btn-ghost flex-1" @click="showBatchPrint = false">关闭</button>
          <button class="btn-brand flex-1" :disabled="batchLoading || batchBills.length === 0" @click="startBatchPrint">开始打印</button>
        </div>
      </div>
    </Modal>

    <!-- 账单详情：全部费用项目逐项列出，附收款流水 -->
    <Modal v-model="showDetail" title="账单详情">
      <div v-if="detailBill">
        <BillDetail :bill="detailBill" :items="detailItems" :payments="detailPayments" :fee-items="feeItems" />
        <div class="flex justify-end mt-4">
          <button class="btn-ghost" @click="showDetail = false">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 批量打印层：预览全部单据，打印时每张一页（工具栏不参与打印） -->
    <div v-if="batchPrinting" class="batch-print fixed inset-0 z-50 overflow-auto bg-white">
      <div class="sticky top-0 z-10 flex items-center justify-end gap-2 px-4 py-2.5 bg-white border-b border-border shadow-sm">
        <span class="text-xs text-muted-foreground mr-auto">共 {{ batchBills.length }} 张单据 · 纸张 {{ paperSummary(receiptPaper, customPaper) }}</span>
        <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <span>纸张</span>
          <ReceiptPaperPicker v-model:paper="receiptPaper" v-model:custom="customPaper" @update:paper="onPaperChange" @update:custom="onPaperChange" />
        </div>
        <button class="btn-brand !py-1.5 !px-3 text-xs" @click="printBatch">🖨 打印</button>
        <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="batchPrinting = false">关闭</button>
      </div>
      <div class="max-w-3xl mx-auto py-5 space-y-6">
        <div v-for="(r, i) in batchBills" :key="i" class="receipt-page">
          <RentalReceipt :bill="r.bill" :property="batchProperty" :items="r.items" />
        </div>
      </div>
    </div>

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
import { computed, nextTick, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DateField from '../components/DateField.vue'
import FilePick from '../components/FilePick.vue'
import FilterPanel from '../components/FilterPanel.vue'
import MeterReadingModal from '../components/MeterReadingModal.vue'
import BillDetail from '../components/BillDetail.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { useLedgerStore } from '../stores/ledger'
import RentalReceipt from '../components/RentalReceipt.vue'
import ReceiptPaperPicker from '../components/ReceiptPaperPicker.vue'
import { toast } from '../utils/toast'
import {
  applyReceiptPaper, loadCustomPaper, loadReceiptPaper, paperSummary, saveReceiptPaper,
  type CustomPaper, type ReceiptPaper,
} from '../utils/receiptPaper'
import {
  defaultBilling, fetchBillingDefaults, meterUnit, roomBillingSummary, type BillingDefaults,
} from '../utils/billing'
import { billTypeLabel, coveredPeriodText, cycleLabel, cycleMonths, payCycleOptions, type PayCycle } from '../utils/cycle'
import {
  arrearsOf, carryInOf, createBill, currentPeriod, deleteBill, downloadTemplate, exportBills, fmtMoney,
  getAllRooms, getBillDetail, getBillReceipts, getBills, getFeeItems, getRooms, importBills, isArrears,
  payableOf, payBill, updateBill,
  type Bill, type BillItemDetail, type FeeItem, type ImportResult, type PropertyMeta, type ReceiptEntry, type RoomView,
} from '../api/rental'

const ledgerStore = useLedgerStore()

const bills = ref<Bill[]>([])
const allRooms = ref<RoomView[]>([])
// 收费项目定义：账单编辑里把排除的 key 翻译成名称
const feeItems = ref<FeeItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const periodFrom = ref('')
const periodTo = ref('')
const billType = ref<'' | PayCycle>('')
const status = ref('')
// 小区/村组 + 房源下拉：房源选项随所选小区收窄（小区条件由服务端按房源反查）
const filterCommunity = ref('')
const filterRoomId = ref(0)
const communityOptions = computed(() => [...new Set(allRooms.value.map((r) => r.community).filter(Boolean))])
const roomOptions = computed(() =>
  filterCommunity.value ? allRooms.value.filter((r) => r.community === filterCommunity.value) : allRooms.value,
)

function onCommunityChange() {
  if (filterRoomId.value && !roomOptions.value.some((r) => r.id === filterRoomId.value)) filterRoomId.value = 0
  load(1)
}
const keyword = ref('')
const saving = ref(false)

// 全局默认（偏好设置 → 租房设置）：抄表预览里电/燃气单价的兜底值
const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })

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
const receiptItems = ref<BillItemDetail[]>([])
const receiptProperty = ref<PropertyMeta>({ name: '', contact: '', note: '' })

// 账单详情：逐项列出费用项目（含自定义）的金额/已收/欠缴与收款流水
const showDetail = ref(false)
const detailBill = ref<Bill | null>(null)
const detailItems = ref<BillItemDetail[]>([])
interface DetailPayment {
  id: number; paid_at: string; amount: number; note: string
  items: { key: string; name: string; amount: number }[]
}
const detailPayments = ref<DetailPayment[]>([])

const showImport = ref(false)
const importing = ref(false)
const importError = ref('')
const importResult = ref<ImportResult | null>(null)
const importFile = ref<File | null>(null)
const importFileName = computed(() => importFile.value?.name || '')

function onPickImportFile(files: File[]) {
  importFile.value = files[0] ?? null
  importResult.value = null
  importError.value = ''
}

const pageSum = computed(() => {
  let t = 0
  let a = 0
  for (const b of bills.value) {
    // 应收与欠缴必须用**同一口径**（都含上期结转），否则会算出
    // 「欠缴 4900 > 应收 3600」这种不可能的显示——欠的钱不可能比应收还多。
    // 原先这里 t 用 total_amount、a 用 arrearsOf（基于 payable），两者基准不同。
    t += payableOf(b)
    a += arrearsOf(b)
  }
  return { total: t, arrears: a }
})

// 手机端筛选摘要行：当前月份 · 状态 · 关键词，以及非默认条件数（徽标）
const billsStatusText: Record<string, string> = { arrears: '欠缴（含部分）', unpaid: '未缴纳', partial: '部分已缴', paid: '已缴清' }
// 筛选摘要里的账单类型名：直接复用 cycle.ts 的 billTypeLabel（月/季/半年/年账单），
// 与筛选下拉、后端 bill_type 取值同源，新增周期时不会再出现摘要与下拉对不上。
const billsTypeText = computed<Record<string, string>>(() =>
  Object.fromEntries(payCycleOptions.map((o) => [o.value, billTypeLabel(o.value)])),
)
const billsFilterSummary = computed(() =>
  [
    periodFrom.value && periodTo.value ? `${periodFrom.value} ~ ${periodTo.value}` : periodFrom.value || periodTo.value || '全部月份',
    billsTypeText.value[billType.value] || null,
    billsStatusText[status.value] || '全部状态',
    filterCommunity.value || null,
    filterRoomId.value ? (allRooms.value.find((r) => r.id === filterRoomId.value)?.room_no || '指定房源') : null,
    keyword.value ? `“${keyword.value}”` : null,
  ].filter(Boolean).join(' · '),
)
// 徽标统计「非默认条件」：账期区间此前漏算，只填区间时列表已被过滤、徽标却是 0
// （2026-10-11 审计）。
const billsFilterCount = computed(() =>
  (periodFrom.value || periodTo.value ? 1 : 0) +
  (billType.value ? 1 : 0) + (status.value ? 1 : 0) + (keyword.value ? 1 : 0) +
  (filterCommunity.value ? 1 : 0) + (filterRoomId.value ? 1 : 0))

const filterRef = ref<InstanceType<typeof FilterPanel> | null>(null)

// 周期角标的 title：显示文字只放周期名（季付/半年付/年付），所以 title 必须补上
// 「周期只管固定费、本单水电仍按月」这层说明——否则年付房在非到期月出的水电
// 账单带着「年付」角标，容易被误读成"这月收了 12 个月租金"。
function cycleBadgeTitle(cycle: string | undefined | null, period: string): string {
  const months = cycleMonths(cycle)
  return `${cycleLabel(cycle)}：固定费每 ${months} 个月收一次，本单水电按月 · 覆盖 ${coveredPeriodText(period, cycle)}`
}

function applyFilters() {
  load(1)
  filterRef.value?.collapse()
}

// meterBlocks 电/燃气抄表块（水费单独成块，因为可按吨/包月切换）
// feeKey 用于租户收费项目排除判断（被排除项目禁用输入并按 0 预估）
const meterBlocks = [
  { label: '电费', feeKey: 'elec', unit: meterUnit('elec'), lastKey: 'elec_last', nowKey: 'elec_now', priceKey: 'elec_price' },
  { label: '燃气费', feeKey: 'gas', unit: meterUnit('gas'), lastKey: 'gas_last', nowKey: 'gas_now', priceKey: 'gas_price' },
] as const

function previewFee(last: number, now: number, price: number): number {
  const usage = now > last ? now - last : 0
  return Math.round(usage * price * 100) / 100
}

// ---------- 租户收费项目排除（账单快照 excluded_fees） ----------

// 台账停用的项目（disabled_fees 快照）：与租户排除项同口径——本期不计费。
// 停用只影响新账单；老账单快照里没有这一项，自然照旧计费。
function feeDisabledByLedger(key: string): boolean {
  return (editForm.value?.disabled_fees ?? []).includes(key)
}

function feeExcluded(key: string): boolean {
  return (editForm.value?.excluded_fees ?? []).includes(key) || feeDisabledByLedger(key)
}

// 排除项目的名称列表（编辑表单顶部提示用）
const editExcludedNames = computed(() => {
  const keys = editForm.value?.excluded_fees ?? []
  return keys.map((k) => feeItems.value.find((f) => f.key === k)?.name ?? k).join('、')
})

// 台账停用项目的名称列表（与上面的租户排除项分开提示：一个是「这户不收」，
// 一个是「整个台账不收」，房东看到的处理方式不同）
const editDisabledNames = computed(() => {
  const keys = editForm.value?.disabled_fees ?? []
  return keys.map((k) => feeItems.value.find((f) => f.key === k)?.name ?? k).join('、')
})

// waterPreview 水费预估：包月取每月固定金额（water_price 存的就是元/月）×
// 账单覆盖月数（季付 ×3 / 半年付 ×6 / 年付 ×12），按吨按用量 × 单价
// ——与服务端 Bill.waterFee 同一口径。
function waterPreview(f: Pick<Bill, 'water_mode' | 'water_last' | 'water_now' | 'water_price' | 'pay_cycle'> | null): number {
  if (!f) return 0
  if (f.water_mode === 'monthly') {
    return Math.round((f.water_price || 0) * cycleMonths(f.pay_cycle) * 100) / 100
  }
  return previewFee(f.water_last, f.water_now, f.water_price)
}

function previewTotal(): number {
  const f = editForm.value
  if (!f) return 0
  // 与服务端 recalc 同口径：被排除的项目按 0 计；押金不计入「排除项」——
  // 它是入住押金，服务端也不受 ExcludedFees 影响，必须原样加上，
  // 否则编辑账单时预览合计会比实际少一个押金。
  const t =
    (feeExcluded('rent') ? 0 : (f.rent || 0)) +
    (feeExcluded('water') ? 0 : waterPreview(f)) +
    (feeExcluded('elec') ? 0 : previewFee(f.elec_last, f.elec_now, f.elec_price)) +
    (feeExcluded('gas') ? 0 : previewFee(f.gas_last, f.gas_now, f.gas_price)) +
    (feeExcluded('sanitation') ? 0 : (f.sanitation_fee || 0)) +
    (feeExcluded('management') ? 0 : (f.management_fee || 0)) +
    (f.deposit_fee || 0)
  return Math.round(t * 100) / 100
}

// 该房当前按租户生效的计费与缴费设置（房源出参 billing），用于账单编辑时提示与预填
const editRoomBilling = computed(() => {
  const room = allRooms.value.find((r) => r.id === editForm.value?.room_id)
  return room ? roomBillingSummary(room.billing) : ''
})

// 指定计费方式下该房配置的金额（与后端 waterAmountForMode 同口径：
// 租户显式设置 → 全局默认），账单里切换方式时按它预填。
function configuredWaterAmount(mode: 'meter' | 'monthly'): number {
  const room = allRooms.value.find((r) => r.id === editForm.value?.room_id)
  if (room?.billing) {
    return mode === 'monthly' ? room.billing.water_monthly_fee : room.billing.water_meter_price
  }
  return mode === 'monthly' ? billingDefaults.value.waterMonthlyFee : billingDefaults.value.waterMeterPrice
}

// 切换水费计费方式：金额按新方式的配置金额填入（元/吨 ↔ 元/月 不能混用）
function onEditWaterModeChange() {
  const f = editForm.value
  if (!f) return
  f.water_price = configuredWaterAmount(f.water_mode === 'monthly' ? 'monthly' : 'meter')
}

function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// 抄表读数文案（带单位）：包月水费不看表读数，直接标"包月"；按吨显示 上月→本月 吨
function waterReadingText(b: Bill, sep = ' → '): string {
  if (b.water_mode === 'monthly') return '包月'
  return `${fmtRead(b.water_last)}${sep}${fmtRead(b.water_now)} ${meterUnit('water')}`
}

// 电/燃气读数文案（带单位）：上月 → 本月
function meterReadingText(b: Bill, which: 'elec' | 'gas'): string {
  const last = which === 'elec' ? b.elec_last : b.gas_last
  const now = which === 'elec' ? b.elec_now : b.gas_now
  return `${fmtRead(last)} → ${fmtRead(now)} ${meterUnit(which)}`
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
      period_from: periodFrom.value || undefined,
      period_to: periodTo.value || undefined,
      bill_type: billType.value || undefined,
      status: status.value, keyword: keyword.value,
      community: filterCommunity.value || undefined,
      room_id: filterRoomId.value || undefined,
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
  // 跨页拉全：服务端 pageSize 硬上限是 100，写死 100 在房源超过 100 间时
  // 会让小区/房源下拉缺项（2026-10-11 审计）。
  allRooms.value = await getAllRooms()
}

function doExport() {
  // 按页面上的账期筛选区间导出（2026-10-11：原先只取「起始或截止」其中一个，
  // 用户选了 2026-10 ~ 2026-11 也只导出一个月的账，是"不能按时间段导出"的根因）。
  // 两段都为空 → 导出全部月份。
  exportBills(undefined, periodFrom.value || undefined, periodTo.value || undefined)
}

const confirmState = ref({ show: false, title: '', message: '', action: null as null | (() => Promise<void>) })

// 抄表开票弹窗入口已迁至「抄表」模块（MeterReadingModal 共享组件）；
// 账单列表点「抄表」仍可打开同一弹窗编辑该账单读数。
const showReading = ref(false)
const readingTarget = ref<Bill | null>(null)

function openReading(b?: Bill) {
  readingTarget.value = b ?? null
  showReading.value = true
}

async function openEdit(b: Bill) {
  editForm.value = { ...b, water_mode: b.water_mode === 'monthly' ? 'monthly' : 'meter' }
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

// 收款弹窗状态：bill_items 明细行（含每项应付/已收/欠缴）+ 勾选收款
const payItems = ref<{ key: string; name: string; kind: string; amount: number; paid: number; arrears: number; detail: string; checked: boolean; payAmount: number }[]>([])

async function openPay(b: Bill) {
  payBillRow.value = { ...b }
  payAmount.value = 0
  payNote.value = ''
  payError.value = ''
  payItems.value = []
  showPay.value = true
  // 拉取费用明细（服务端算好每项应付/已收/欠缴），勾选金额默认填欠缴额。
  const res = await getBillDetail(b.id)
  if (res.data?.code === 0) {
    const rows = (res.data.data.items || []) as BillItemDetail[]
    payItems.value = rows.map((it) => ({
      key: it.key, name: it.name, kind: it.kind,
      amount: it.amount, paid: it.paid, arrears: it.arrears, detail: it.detail,
      checked: it.arrears > 0.005,
      payAmount: Math.round(it.arrears * 100) / 100,
    }))
    payAmount.value = Math.round(payItems.value.reduce((s, it) => it.checked ? s + it.payAmount : s, 0) * 100) / 100
  }
}

function onPayItemCheck(it: { checked: boolean; payAmount: number; arrears: number }) {
  if (it.checked && !(it.payAmount > 0)) it.payAmount = Math.round(it.arrears * 100) / 100
  payAmount.value = Math.round(payItems.value.reduce((s, x) => x.checked ? s + (x.payAmount || 0) : s, 0) * 100) / 100
}

// 勾选全部欠缴项目：金额填各项欠缴额，合计联动
function fillAllArrears() {
  for (const it of payItems.value) {
    if (it.arrears > 0.005) {
      it.checked = true
      it.payAmount = Math.round(it.arrears * 100) / 100
    }
  }
  payAmount.value = Math.round(payItems.value.reduce((s, x) => x.checked ? s + (x.payAmount || 0) : s, 0) * 100) / 100
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
    const items = payItems.value
      .filter((it) => it.checked && it.payAmount > 0)
      .map((it) => ({ key: it.key, name: it.name, amount: Math.round(it.payAmount * 100) / 100 }))
    // 有明细的账单必须按项目分摊（合计=收款额）；历史账单（无明细）直接整额收。
    if (payItems.value.length > 0 && items.length === 0) {
      payError.value = '请先勾选本期收款的项目'
      return
    }
    if (items.length > 0) {
      const sum = Math.round(items.reduce((s, x) => s + x.amount, 0) * 100) / 100
      if (Math.abs(sum - payAmount.value) > 0.005) {
        payError.value = '勾选项目的金额合计应等于本次收款合计'
        return
      }
    }
    const res = await payBill(row.id, payAmount.value, payNote.value, items.length ? items : undefined)
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
    receiptItems.value = res.data.data.items || []
    receiptProperty.value = res.data.data.property
    showReceipt.value = true
    // 打开弹窗就落一次 @page，这样用浏览器菜单「打印 / 另存 PDF」（Ctrl+P）
    // 走的是同一张纸；此前只在点应用按钮时写入，直接 Ctrl+P 会退回兜底的 A5。
    applyReceiptPaper(receiptPaper.value, customPaper.value)
  }
}

// 打开账单详情：全部费用项目与收款流水
async function openDetail(b: Bill) {
  const res = await getBillDetail(b.id)
  if (res.data?.code === 0) {
    detailBill.value = res.data.data.bill
    detailItems.value = res.data.data.items || []
    detailPayments.value = res.data.data.payments || []
    showDetail.value = true
  }
}

function printReceipt() {
  applyReceiptPaper(receiptPaper.value, customPaper.value)
  window.print()
}

// ---------- 批量打印收费单据 ----------

// 单据纸张：打印设备属性，保存在本机（localStorage），单张与批量共用
const receiptPaper = ref<ReceiptPaper>(loadReceiptPaper())
// 自定义纸张尺寸（针式打印纸/发货单）：与预设共存，选中「自定义尺寸…」时生效。
const customPaper = ref<CustomPaper>(loadCustomPaper())
function onPaperChange() {
  saveReceiptPaper(receiptPaper.value)
  // 立即重写 @page，让预览层的工具栏改动当场生效
  applyReceiptPaper(receiptPaper.value, customPaper.value)
}

const showBatchPrint = ref(false)
const batchLoading = ref(false)
const batchError = ref('')
const batchFrom = ref('')
const batchTo = ref('')
const batchQueried = ref(false)
const batchBills = ref<ReceiptEntry[]>([])
const batchProperty = ref<PropertyMeta>({ name: '', contact: '', note: '' })
const batchPrinting = ref(false)

function openBatchPrint() {
  batchFrom.value = currentPeriod()
  batchTo.value = currentPeriod()
  batchQueried.value = false
  batchBills.value = []
  batchError.value = ''
  showBatchPrint.value = true
}

async function previewReceipts() {
  batchLoading.value = true
  batchError.value = ''
  try {
    const res = await getBillReceipts(batchFrom.value, batchTo.value)
    const env = res.data as unknown as { code: number; message: string; data?: { property: PropertyMeta; bills: ReceiptEntry[] } }
    if (env?.code === 0 && env.data) {
      batchBills.value = env.data.bills || []
      batchProperty.value = env.data.property || { name: '', contact: '', note: '' }
      batchQueried.value = true
    } else {
      batchError.value = env?.message || '查询失败'
    }
  } catch {
    batchError.value = '查询失败，请重试'
  } finally {
    batchLoading.value = false
  }
}

// 进入全屏预览层并直接拉起打印；纸张在打印时写入 @page。
// 必须先关掉配置弹窗：Modal 是 teleport 到 body 的 z-50，DOM 顺序在全屏
// 预览层（#app 内）之后，不关会叠在预览层上面挡住单据（粉丝反馈"打印要
// 取消 2 次才出"的界面来源之一）。
async function startBatchPrint() {
  if (!batchBills.value.length) return
  showBatchPrint.value = false
  batchPrinting.value = true
  await nextTick()
  printBatch()
}

function printBatch() {
  applyReceiptPaper(receiptPaper.value, customPaper.value)
  window.print()
}

async function doImport() {
  const file = importFile.value
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
  billingDefaults.value = await fetchBillingDefaults()
  try {
    const res = await getFeeItems()
    if (res.data?.code === 0) feeItems.value = res.data.data ?? []
  } catch {
    feeItems.value = [] // 加载失败时排除项名称直接显示 key
  }
  await Promise.all([loadRooms(), load(1)])
})
</script>
