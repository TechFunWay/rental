<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="租户管理" description="入住登记与退租管理，宿舍场景支持一房多名租户">
      <template #actions>
        <button class="btn-ghost relative" @click="openApplications">
          入住申请
          <span v-if="pendingCount" class="absolute -top-1.5 -right-1.5 min-w-[18px] h-[18px] px-1 rounded-full bg-rose-500 text-white text-[10px] font-bold flex items-center justify-center tabular-nums">{{ pendingCount > 9 ? '9+' : pendingCount }}</span>
        </button>
        <!-- 租户提交的资料变更申请（v0.6.10）：待审时带红点，点开逐项对照后通过/驳回 -->
        <button class="btn-ghost relative" @click="showChangeReview = true">
          资料变更申请
          <span v-if="changePendingCount" class="absolute -top-1.5 -right-1.5 min-w-[18px] h-[18px] px-1 rounded-full bg-rose-500 text-white text-[10px] font-bold flex items-center justify-center tabular-nums">{{ changePendingCount > 9 ? '9+' : changePendingCount }}</span>
        </button>
        <!-- 导出：派出所场景按当前筛选条件导出租户 CSV（只读操作，查看档即可见） -->
        <button v-if="ledgerStore.canView('tenants')" class="btn-ghost" @click="exportList">导出</button>
        <button v-if="ledgerStore.canFull('tenants')" class="btn-ghost" @click="showImport = true">导入</button>
        <!-- 粘贴识别：外网不通、登记链接打不开时，租客把资料微信发过来，在这里粘贴识别入库 -->
        <button v-if="ledgerStore.canFull('tenants')" class="btn-ghost" @click="showPaste = true">粘贴识别</button>
        <button v-if="ledgerStore.canEdit('tenants')" class="btn-brand" @click="openCreate">＋ 登记租户</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="tenantsFilterSummary" :active-count="tenantsFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <input v-model="keyword" class="input-field flex-1 min-w-[200px] !py-2" placeholder="搜索姓名 / 电话 / 标签…" @keyup.enter="applyFilters" />
        <select v-model="active" class="input-field !py-2 !w-auto" @change="load(1)">
          <option value="">全部</option>
          <option value="1">在租</option>
          <option value="0">已退租</option>
        </select>
        <select v-model="community" class="input-field !py-2 !w-auto max-w-[150px]" @change="onCommunityChange">
          <option value="">全部小区</option>
          <option v-for="c in communityOptions" :key="c" :value="c">{{ c }}</option>
        </select>
        <select v-model="roomId" class="input-field !py-2 !w-auto max-w-[150px]" @change="load(1)">
          <option :value="0">全部房源</option>
          <option v-for="r in roomOptions" :key="r.id" :value="r.id">{{ r.room_no }}</option>
        </select>
        <select v-model="filterTag" class="input-field !py-2 !w-auto max-w-[130px]" @change="load(1)">
          <option value="">全部标签</option>
          <option v-for="t in tagOptions" :key="t" :value="t">{{ t }}</option>
        </select>
        <button class="btn-brand !py-2" @click="applyFilters">搜索</button>
      </div>
    </FilterPanel>

    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="tenants.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        暂无租户，点击右上角「登记租户」或「导入」批量登记。
      </div>
      <template v-else>
      <!-- 桌面端：表格（列多，最小宽度防挤压竖排，窄桌面走容器横向滚动） -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm min-w-[1080px]">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th class="py-3 px-4 font-medium whitespace-nowrap">姓名</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">电话</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">房源</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">计费与缴费</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">入住日期</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">到期时间</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">退租日期</th>
              <th class="py-3 px-4 font-medium text-right whitespace-nowrap">押金（元）</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">状态</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">备注</th>
              <th class="py-3 px-4 font-medium text-right whitespace-nowrap">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="t in tenants" :key="t.id" class="hover:bg-muted/30 transition-colors">
              <td class="py-3 px-4">
                <div class="font-semibold text-foreground">{{ t.name }}</div>
                <div v-if="t.tags?.length" class="flex flex-wrap gap-1 mt-1">
                  <span v-for="tag in t.tags" :key="tag" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ tag }}</span>
                </div>
              </td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.phone || '—' }}</td>
              <td class="py-3 px-4 text-foreground" :title="t.room_label">{{ t.room_label || t.room_no || '—' }}</td>
              <td class="py-3 px-4 text-muted-foreground text-xs">{{ tenantBillingSummary(t, billingDefaults) }}<span v-if="tenantExcludedText(t)">{{ tenantExcludedText(t) }}</span></td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_in_date || '—' }}</td>
              <td class="py-3 px-4 tabular-nums">
                <template v-if="t.lease_end_date">
                  <div class="text-muted-foreground">{{ t.lease_end_date }}</div>
                  <div v-if="leaseStatus(t)" class="text-xs font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</div>
                </template>
                <span v-else class="text-muted-foreground">—</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_out_date || '—' }}</td>
              <td class="py-3 px-4 text-right tabular-nums" :class="t.deposit ? 'text-foreground' : 'text-muted-foreground'">
                <div>{{ t.deposit ? fmtDeposit(t.deposit) : '—' }}</div>
                <!-- 押金单照片：押金是实物凭证，单独入口查看/补拍（2026-10-17 粉丝反馈） -->
                <button
                  v-if="t.deposit || t.deposit_photos_count"
                  class="mt-0.5 text-[11px] font-normal text-brand-600 dark:text-brand-300 hover:underline"
                  @click="openDepositPhotos(t)"
                >押金单{{ t.deposit_photos_count ? ` ${t.deposit_photos_count}` : ' +' }}</button>
              </td>
              <td class="py-3 px-4">
                <span v-if="t.active" class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
                <span v-else class="badge bg-muted text-muted-foreground">已退租</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground max-w-[180px] truncate" :title="t.notes">{{ t.notes || '—' }}</td>
              <td class="py-3 px-4 text-right whitespace-nowrap">
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openContracts(t)">
                  合同<span v-if="t.contracts_count" class="ml-0.5 tabular-nums">{{ t.contracts_count }}</span>
                </button>
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openAttachments(t)">
                  附件<span v-if="t.attachments_count" class="ml-0.5 tabular-nums">{{ t.attachments_count }}</span>
                </button>
                <!-- 住户成员：只登记身份、不参与计费；0 人时也留入口（随时补录） -->
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openMembers(t)">
                  {{ t.members_count ? `成员 ${t.members_count}` : '＋成员' }}
                </button>
                <!-- 续租：老租户续半年/一年是最常见的动作，入口放在退租前面（2026-10-20 粉丝需求） -->
                <button v-if="ledgerStore.canEdit('tenants') && t.active" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openRenew(t)">续租</button>
                <button v-if="ledgerStore.canEdit('tenants') && t.active" class="text-amber-600 dark:text-amber-300 hover:underline mr-3" @click="checkout(t)">退租</button>
                <button v-if="ledgerStore.canEdit('tenants')" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openAccount(t)">
                  账号<span v-if="t.account_user_id" class="ml-0.5 text-emerald-600 dark:text-emerald-400" title="已关联登录账号">✓</span>
                </button>
                <button v-if="ledgerStore.canEdit('tenants')" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(t)">编辑</button>
                <button v-if="ledgerStore.canFull('tenants')" class="text-destructive hover:underline" @click="remove(t)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表（紧凑版） -->
      <div class="md:hidden p-2 space-y-1.5">
        <div v-for="t in tenants" :key="t.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ t.name }}</span>
              <span class="text-[11px] text-muted-foreground ml-1.5 tabular-nums">{{ t.phone || '' }}</span>
              <div v-if="t.tags?.length" class="flex flex-wrap gap-1 mt-1">
                <span v-for="tag in t.tags" :key="tag" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ tag }}</span>
              </div>
            </div>
            <span v-if="t.active" class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
            <span v-else class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">已退租</span>
          </div>

          <div class="text-[11px] leading-5 text-muted-foreground">
            <div>
              房源 <span class="text-foreground">{{ t.room_label || t.room_no || '—' }}</span><template v-if="t.deposit"> · 押金 <span class="text-foreground tabular-nums">{{ fmtDeposit(t.deposit) }} 元</span></template>
              <button
                v-if="t.deposit || t.deposit_photos_count"
                class="ml-1 text-brand-600 dark:text-brand-300"
                @click="openDepositPhotos(t)"
              >押金单{{ t.deposit_photos_count ? ` ${t.deposit_photos_count}` : ' +' }}</button>
            </div>
            <div>计费 {{ tenantBillingSummary(t, billingDefaults) }}<span v-if="tenantExcludedText(t)">{{ tenantExcludedText(t) }}</span></div>
            <div class="tabular-nums">入住 {{ t.move_in_date || '—' }} · 到期 {{ t.lease_end_date || '—' }}<template v-if="t.move_out_date"> · 退租 {{ t.move_out_date }}</template>
              <span v-if="leaseStatus(t)" class="ml-1 font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</span>
            </div>
            <div v-if="t.notes" class="truncate" :title="t.notes">{{ t.notes }}</div>
          </div>

          <div class="flex items-center gap-3 pt-0.5 border-t border-border/60 text-xs">
            <button class="text-brand-600 dark:text-brand-300" @click="openContracts(t)">
              合同<span v-if="t.contracts_count" class="ml-0.5 tabular-nums">{{ t.contracts_count }}</span>
            </button>
            <button class="text-brand-600 dark:text-brand-300" @click="openAttachments(t)">
              附件<span v-if="t.attachments_count" class="ml-0.5 tabular-nums">{{ t.attachments_count }}</span>
            </button>
            <button class="text-brand-600 dark:text-brand-300" @click="openMembers(t)">
              {{ t.members_count ? `成员 ${t.members_count}` : '成员' }}
            </button>
            <button v-if="ledgerStore.canEdit('tenants') && t.active" class="text-brand-600 dark:text-brand-300" @click="openRenew(t)">续租</button>
            <button v-if="ledgerStore.canEdit('tenants') && t.active" class="text-amber-600 dark:text-amber-300" @click="checkout(t)">退租</button>
            <button v-if="ledgerStore.canEdit('tenants')" class="text-brand-600 dark:text-brand-300" @click="openAccount(t)">
              账号<span v-if="t.account_user_id" class="ml-0.5 text-emerald-600 dark:text-emerald-400">✓</span>
            </button>
            <button v-if="ledgerStore.canEdit('tenants')" class="text-brand-600 dark:text-brand-300" @click="openEdit(t)">编辑</button>
            <button v-if="ledgerStore.canFull('tenants')" class="text-destructive ml-auto" @click="remove(t)">删除</button>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 新增/编辑弹窗 -->
    <Modal v-model="showModal" :title="editing ? '编辑租户' : '登记租户'">
      <form class="space-y-4" @submit.prevent="save">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">姓名 *</label>
            <input v-model="form.name" class="input-field" placeholder="租户姓名" required />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">电话</label>
            <input v-model="form.phone" class="input-field" placeholder="选填" />
          </div>
        </div>
        <!-- 身份证号独占一行，选填 -->
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">身份证号</label>
          <input v-model="form.id_card" class="input-field" placeholder="选填" />
        </div>
        <!-- 身份证住址：v0.6.10 起落到租户档案上（此前只在登记申请里，转入登记后就丢了），
             租客也能在门户里提交申请修改、由房东在这里审阅后生效。 -->
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">身份证住址</label>
          <input v-model="form.address" class="input-field" placeholder="选填" />
        </div>
        <!-- 紧急联系人：租客可在门户申请修改 -->
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">紧急联系人</label>
            <input v-model="form.emergency_contact" class="input-field" placeholder="选填" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">紧急联系电话</label>
            <input v-model="form.emergency_phone" class="input-field" placeholder="选填" />
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">标签</label>
          <TagInput v-model="formTags" :suggestions="tagOptions" placeholder="如 优质 / 中介介绍 / 长租，回车添加" />
          <p class="text-[11px] text-muted-foreground mt-1">自由打标，方便按标签分组和筛选租户。</p>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">所属房源 *</label>
          <select v-model.number="form.room_id" class="input-field" required>
            <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ roomLabelOf(r) }}</option>
          </select>
          <p v-if="allRooms.length === 0" class="text-xs text-destructive mt-1">请先在「房源管理」中添加房源</p>
        </div>
        <!-- 计费与缴费：每个租户可以不一样，未设置时跟随偏好设置里的全局默认 -->
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <div>
            <p class="text-xs font-semibold text-muted-foreground">计费与缴费（按租户设置，留空跟随全局默认）</p>
            <p class="text-[11px] text-muted-foreground mt-1">当前全局默认：{{ globalBillingText(billingDefaults) }}</p>
          </div>
          <!-- 收费项目勾选：勾选 = 参与该租户的账单结算，未勾选项目出账费用为 0 -->
          <div>
            <p class="text-xs text-muted-foreground block mb-1.5">收费项目（勾选 = 参与本租户的账单结算）</p>
            <div class="flex flex-wrap gap-1.5">
              <label
                v-for="fi in billableFeeItems" :key="fi.key"
                class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs cursor-pointer select-none transition-colors"
                :class="feeChecked(fi.key) ? 'border-brand-500/60 bg-brand-500/10 text-foreground' : 'border-border text-muted-foreground'"
              >
                <input type="checkbox" class="w-3.5 h-3.5 accent-indigo-500 shrink-0" :checked="feeChecked(fi.key)" @change="toggleFee(fi.key)" />
                {{ fi.name }}
              </label>
            </div>
            <p v-if="excludedFeeNames" class="text-[11px] text-muted-foreground mt-1.5">
              不参与：{{ excludedFeeNames }}，出账时这些项目费用为 0；之后新增的收费项目默认参与，需排除再回来取消勾选。
            </p>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">水费计费方式</label>
              <select v-model="form.water_mode" class="input-field !py-2" @change="onWaterModeChange">
                <option value="">跟随全局</option>
                <option value="meter">按吨计价</option>
                <option value="monthly">包月</option>
              </select>
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">
                水费{{ formWaterMode === 'monthly' ? '包月金额（元/月）' : '单价（元/吨）' }}
              </label>
              <!-- 跟随全局时只展示全局默认值（灰色只读）；显式选择后可改，清空或 0 = 跟随全局 -->
              <input
                v-if="formWaterFollows"
                :value="formWaterGlobalAmount.toFixed(2)"
                type="number" class="input-field !py-2 bg-muted/60 text-muted-foreground" disabled
              />
              <input
                v-else-if="formWaterMode === 'monthly'"
                v-model.number="form.water_monthly_fee" type="number" step="0.01" min="0"
                class="input-field !py-2" placeholder="如 40，0 = 跟随全局"
              />
              <input
                v-else
                v-model.number="form.water_price" type="number" step="0.01" min="0"
                class="input-field !py-2" placeholder="如 5，0 = 跟随全局"
              />
            </div>
          </div>
          <!-- 手机端两列换行排布（第三项落到下一行），桌面端保持一行三列 -->
          <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">缴费周期</label>
              <select v-model="form.pay_cycle" class="input-field !py-2">
                <option value="">跟随全局</option>
                <!-- 周期选项集中在 utils/cycle.ts（月付/季付/半年付/年付），
                     新增周期只改那一处，页面不再写字面量 -->
                <option v-for="o in payCycleOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
              </select>
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">缴费日（几号）</label>
              <input
                v-model="payDayInput" type="number" min="0" max="28" class="input-field !py-2"
                placeholder="跟随全局"
              />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">提前提醒天数</label>
              <input
                v-model="remindDaysInput" type="number" min="0" max="30" class="input-field !py-2"
                placeholder="跟随全局"
              />
            </div>
          </div>
          <p class="text-[11px] text-muted-foreground">
            当前全局：{{ waterModeLabel(billingDefaults.waterMode) }} {{ formWaterGlobalAmount.toFixed(2) }} 元 · {{ cycleLabel(billingDefaults.payCycle) }} · {{ payDayText(billingDefaults.payDay) }} · 提前 {{ billingDefaults.remindDays }} 天提醒。
            {{ formWaterFollows
              ? ''
              : (formWaterMode === 'monthly'
                ? '水费按每月固定金额计，不抄表；金额留空或 0 = 跟随全局默认'
                : '水费按用量 × 单价计；单价留空或 0 = 跟随全局默认') }}
            缴费周期决定固定费（租金/卫生费/管理费）多久收一次；水/电/燃气仍按月抄表出账。缴费日留空或 0 表示不提醒，1-28 号按日提醒。
          </p>
        </div>
        <!-- 手机端两列换行排布，桌面端一行三列 -->
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">入住日期</label>
            <DateField v-model="form.move_in_date" type="date" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">到期时间</label>
            <DateField v-model="form.lease_end_date" type="date" />
            <!-- 租期快捷 chip：半年 / 一年最常用，按入住日期一键算到期日（2026-10-20 粉丝需求） -->
            <LeaseTermChips
              class="mt-1.5"
              :model-value="formLeaseTerm"
              :base-date="form.move_in_date || ''"
              base-label="入住日期"
              @update:model-value="onTenantFormTerm"
            />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">押金（元）</label>
            <input v-model.number="form.deposit" type="number" step="0.01" min="0" class="input-field" placeholder="0" />
          </div>
        </div>
        <!-- 押金单照片（选填）：押金不能只录一个数字，实物单子拍照留档，
             换房东/退租结算/对账时都能拿出来对。新租户保存后自动上传。 -->
        <div class="rounded-xl border border-border p-3">
          <TenantDepositPhotos
            :tenant-id="editing?.id ?? 0"
            :can-edit="ledgerStore.canEdit('tenants')"
            v-model:pending="depositFiles"
          />
        </div>
        <!-- 入住成员：登记租户时就一起录，与租户同一事务保存。
             只登记身份、不参与计费（费用仍只按租户算）；「＋」加一行、行内可改可删，
             保存时以这份列表为准（删掉的行保存后即从档案里移除）。 -->
        <div class="rounded-xl border border-border p-3 space-y-2">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <span class="text-xs font-semibold text-muted-foreground">入住成员（选填）</span>
              <p class="text-[11px] text-muted-foreground mt-0.5">
                同住的家属或合住人在这里登记，<span class="text-foreground">费用仍只按租户算</span>。
              </p>
            </div>
            <button type="button" class="btn-brand !py-1.5 !px-3 text-xs shrink-0" @click="addMemberRow">＋ 添加成员</button>
          </div>

          <p v-if="formMembers.length === 0" class="text-[11px] text-muted-foreground py-0.5">
            只住租户本人时不填即可；有同住人时点右上角「＋ 添加成员」。
          </p>

          <div v-for="(m, i) in formMembers" :key="m.uid" class="rounded-lg bg-muted/60 p-2.5 space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-[11px] font-medium text-muted-foreground">成员 {{ i + 1 }}</span>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="text-[11px] px-2 py-0.5 rounded-md border transition-colors"
                  :class="m.active !== false
                    ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-600 dark:text-emerald-300'
                    : 'bg-muted border-border text-muted-foreground'"
                  @click="m.active = m.active === false"
                >{{ m.active !== false ? '在住' : '已搬离' }}</button>
                <button type="button" class="text-[11px] text-destructive" @click="removeMemberRow(i)">删除</button>
              </div>
            </div>
            <div class="grid grid-cols-2 gap-2">
              <input v-model="m.name" class="input-field !py-2" placeholder="姓名 *" />
              <input v-model="m.relation" class="input-field !py-2" list="member-relations" placeholder="与租户关系" />
              <input v-model="m.phone" class="input-field !py-2" inputmode="tel" placeholder="手机号（选填）" />
              <input v-model="m.id_card" class="input-field !py-2 tabular-nums" placeholder="身份证号（选填）" />
            </div>
            <p v-if="idCardHint(m.id_card || '')" class="text-[11px] text-muted-foreground">{{ idCardHint(m.id_card || '') }}</p>
          </div>
          <datalist id="member-relations">
            <option v-for="r in MEMBER_RELATIONS" :key="r" :value="r" />
          </datalist>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <input v-model="form.notes" class="input-field" placeholder="选填" />
        </div>
        <p v-if="formError" class="text-sm text-destructive">{{ formError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showModal = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 入住申请审核：租客通过登记链接提交（公开页），确认后转入租户 -->
    <Modal v-model="showApplications" title="入住申请">
      <div class="space-y-3">
        <p class="text-xs text-muted-foreground leading-relaxed">
          登记链接在「房源管理 → 登记链接」生成，发给租客后其提交的资料出现在这里。
          「转入登记」会带出姓名/电话/证件并预选房源，补齐计费设置后保存即入库，身份证照片自动存入租户附件。
        </p>
        <div v-if="applicationsLoading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="applications.length === 0" class="py-8 text-center text-sm text-muted-foreground">
          暂无申请。把登记链接发给租客，等 TA 提交后这里就会出现。
        </div>
        <div v-else class="space-y-2.5 max-h-[60vh] overflow-auto pr-0.5">
          <div v-for="a in applications" :key="a.id" class="rounded-xl border border-border p-3">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="text-sm font-semibold text-foreground">
                  {{ a.name }}
                  <span class="text-muted-foreground font-normal text-xs tabular-nums ml-1">{{ a.phone }}</span>
                </div>
                <div class="text-[11px] text-muted-foreground mt-0.5">房源 {{ a.room_label || '—' }} · {{ fmtAppTime(a.created_at) }}</div>
                <div v-if="a.id_card" class="text-[11px] text-muted-foreground mt-0.5 tabular-nums">证件 {{ a.id_card }}</div>
                <div v-if="a.address" class="text-[11px] text-muted-foreground mt-0.5">住址 {{ a.address }}</div>
                <div v-if="a.remark" class="text-[11px] text-muted-foreground mt-0.5">留言：{{ a.remark }}</div>
              </div>
              <span class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]"
                :class="a.status === 'pending' ? 'bg-amber-500/10 text-amber-600 dark:text-amber-300' : a.status === 'approved' ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300' : 'bg-muted text-muted-foreground'">
                {{ a.status === 'pending' ? '待审核' : a.status === 'approved' ? '已入库' : '已拒绝' }}
              </span>
            </div>
            <div v-if="a.photo_url" class="mt-2">
              <img v-if="photoUrls[a.id]" :src="photoUrls[a.id]" class="max-h-52 rounded-lg border border-border" alt="身份证照片" />
              <button v-else class="text-xs text-brand-600 dark:text-brand-300" @click="loadApplicationPhoto(a)">📷 查看身份证照片</button>
            </div>
            <div v-if="a.status === 'pending'" class="flex items-center gap-4 pt-2 mt-2 border-t border-border/60 text-xs">
              <button v-if="ledgerStore.canEdit('tenants')" class="text-brand-600 dark:text-brand-300 font-medium" @click="convertToTenant(a)">转入登记</button>
              <button v-if="ledgerStore.canEdit('tenants')" class="text-destructive" @click="rejectApplication(a)">拒绝</button>
            </div>
            <div v-else-if="a.status === 'approved'" class="pt-2 mt-2 border-t border-border/60 text-[11px] text-emerald-600 dark:text-emerald-400">已转入租户</div>
            <div v-else class="pt-2 mt-2 border-t border-border/60 text-[11px] text-muted-foreground">已拒绝{{ a.review_note ? `：${a.review_note}` : '' }}</div>
          </div>
        </div>
        <div class="flex justify-end">
          <button class="btn-ghost !py-2 !px-4 text-sm" @click="showApplications = false">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 租户资料附件弹窗：租房协议、汽车车牌、租客本人照片、身份证等 -->
    <Modal v-model="showAttachments" title="租户资料附件">
      <div class="space-y-4">
        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-xs text-muted-foreground leading-relaxed">
          <span class="font-semibold text-foreground">{{ attachmentTenantLabel }}</span> ·
          存放租房协议、汽车车牌、租客本人照片、身份证、押金单等留档材料，支持拍照、图片与 PDF。
          单个 ≤20MB，每租户最多 40 个；删租户时一并清理。
        </div>

        <div v-if="ledgerStore.canEdit('tenants')" class="space-y-2">
          <!-- 用途备注：整批打一个标签，方便以后在一堆图里认出哪张是什么。
               用按钮组而不是输入框——常见用途就这几类，点一下比打字快。 -->
          <div>
            <span class="text-xs font-semibold text-muted-foreground block mb-1.5">用途备注（选填）</span>
            <div class="flex flex-wrap gap-1.5">
              <button
                v-for="opt in ATTACHMENT_LABELS"
                :key="opt"
                type="button"
                class="px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors"
                :class="attachmentLabel === opt
                  ? 'bg-brand-500/15 border-brand-500/40 text-brand-600 dark:text-brand-300'
                  : 'bg-surface/70 border-border text-muted-foreground hover:bg-muted'"
                @click="attachmentLabel = attachmentLabel === opt ? '' : opt"
              >{{ opt }}</button>
            </div>
          </div>
          <FilePick
            accept="image/*,.pdf"
            multiple
            :disabled="attachmentUploading"
            :label="attachmentUploading ? '上传中…' : '点击选择图片或 PDF'"
            hint="支持拍照、相册图片或 PDF，可多选；单个 ≤20MB，每租户最多 40 个"
            @change="uploadAttachmentFiles"
          />
        </div>

        <div v-if="attachmentLoading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="attachments.length === 0" class="py-6 text-center text-sm text-muted-foreground">
          还没有上传资料。用手机拍下协议、车牌、租客照片或身份证即可留档。
        </div>
        <ul v-else class="space-y-1.5">
          <li v-for="at in attachments" :key="at.id" class="flex items-center gap-2 rounded-lg bg-muted/60 px-2.5 py-2">
            <span
              class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]"
              :class="at.mime_type === 'application/pdf' ? 'bg-rose-500/10 text-rose-600 dark:text-rose-300' : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            >{{ at.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</span>
            <div class="min-w-0 flex-1">
              <div class="text-xs font-medium text-foreground truncate" :title="at.file_name">{{ at.file_name }}</div>
              <div class="text-[11px] text-muted-foreground tabular-nums">
                <span v-if="at.label" class="text-brand-600 dark:text-brand-300">{{ at.label }} · </span>{{ fmtSize(at.file_size) }} · {{ fmtDate(at.created_at) }}
              </div>
            </div>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="previewAttachment(at)">预览</button>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="downloadAttachmentFile(at)">下载</button>
            <button v-if="ledgerStore.canFull('tenants')" class="text-destructive text-xs shrink-0" @click="removeAttachment(at)">删除</button>
          </li>
        </ul>

        <p v-if="attachmentError" class="text-sm text-destructive">{{ attachmentError }}</p>

        <!-- 应用内预览：图片直接渲染，PDF 内嵌 -->
        <div v-if="attachmentPreviewing && attachmentPreviewUrl" class="space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-foreground truncate">预览：{{ attachmentPreviewing.file_name }}</span>
            <button class="text-muted-foreground hover:text-foreground text-xs" @click="closeAttachmentPreview">收起预览</button>
          </div>
          <iframe
            v-if="attachmentPreviewing.mime_type === 'application/pdf'"
            :src="attachmentPreviewUrl"
            class="w-full h-[420px] rounded-xl border border-border bg-white"
            title="附件预览"
          ></iframe>
          <img v-else :src="attachmentPreviewUrl" class="w-full max-h-[420px] object-contain rounded-xl border border-border bg-muted/40" alt="附件预览" />
        </div>

        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="closeAttachments">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 押金单照片弹窗：押金不能只录一个数字，实物单子单独留档入口 -->
    <Modal v-model="showDepositPhotos" title="押金单照片">
      <div v-if="depositTenant" class="space-y-3">
        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-xs text-muted-foreground leading-relaxed">
          <span class="font-semibold text-foreground">{{ depositTenant.name }}</span>
          · {{ depositTenant.room_label || depositTenant.room_no || '—' }}
          · 押金 <span class="font-semibold text-foreground tabular-nums">{{ fmtDeposit(depositTenant.deposit) }} 元</span>
          <p class="mt-1">拍下押金单实物留档，退租结算、换房东、对账时都有据可查；照片按租户保存，换租客不会丢。</p>
        </div>
        <TenantDepositPhotos
          :tenant-id="depositTenant.id"
          :can-edit="ledgerStore.canEdit('tenants')"
          @changed="onDepositPhotosChanged"
        />
        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="showDepositPhotos = false">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 合同存档弹窗 -->
    <Modal v-model="showContracts" title="租赁合同">
      <div class="space-y-4">
        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-xs text-muted-foreground leading-relaxed flex items-start justify-between gap-3">
          <span>
            <span class="font-semibold text-foreground">{{ contractTenantLabel }}</span> ·
            上传合同拍照、扫描件或 PDF（单个 ≤20MB，每租户最多 20 份），退租后记录保留。
          </span>
          <!-- 合同打印（2026-10-20 粉丝需求）：模板合同就地生成，已上传件按原文件打印 -->
          <button
            type="button"
            class="text-brand-600 dark:text-brand-300 hover:underline shrink-0 whitespace-nowrap"
            @click="openContractPrint"
          >打印合同</button>
        </div>
        <div v-if="ledgerStore.canEdit('tenants')">
          <span class="text-xs font-semibold text-muted-foreground block mb-1.5">上传合同文件</span>
          <FilePick
            accept="image/*,.pdf"
            multiple
            :disabled="contractUploading"
            :label="contractUploading ? '上传中…' : '点击选择合同文件'"
            hint="支持拍照、图片或 PDF，可多选；单个 ≤20MB，每租户最多 20 份"
            @change="uploadContractFiles"
          />
        </div>

        <div v-if="contractLoading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="contracts.length === 0" class="py-6 text-center text-sm text-muted-foreground">
          还没有上传合同。用手机拍下签好的合同页，或上传扫描件 / PDF。
        </div>
        <ul v-else class="space-y-1.5">
          <li v-for="ct in contracts" :key="ct.id" class="flex items-center gap-2 rounded-lg bg-muted/60 px-2.5 py-2">
            <span
              class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]"
              :class="ct.mime_type === 'application/pdf' ? 'bg-rose-500/10 text-rose-600 dark:text-rose-300' : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            >{{ ct.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</span>
            <div class="min-w-0 flex-1">
              <div class="text-xs font-medium text-foreground truncate" :title="ct.file_name">{{ ct.file_name }}</div>
              <div class="text-[11px] text-muted-foreground tabular-nums">{{ fmtSize(ct.file_size) }} · {{ fmtDate(ct.created_at) }}</div>
            </div>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="previewContract(ct)">预览</button>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="download(ct)">下载</button>
            <button v-if="ledgerStore.canFull('tenants')" class="text-destructive text-xs shrink-0" @click="removeContract(ct)">删除</button>
          </li>
        </ul>

        <p v-if="contractError" class="text-sm text-destructive">{{ contractError }}</p>

        <!-- 应用内预览：图片直接渲染，PDF 内嵌 -->
        <div v-if="previewing && previewUrl" class="space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-foreground truncate">预览：{{ previewing.file_name }}</span>
            <button class="text-muted-foreground hover:text-foreground text-xs" @click="closePreview">收起预览</button>
          </div>
          <iframe
            v-if="previewing.mime_type === 'application/pdf'"
            :src="previewUrl"
            class="w-full h-[420px] rounded-xl border border-border bg-white"
            title="合同预览"
          ></iframe>
          <img v-else :src="previewUrl" class="w-full max-h-[420px] object-contain rounded-xl border border-border bg-muted/40" alt="合同预览" />
        </div>

        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="closeContracts">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 批量导入弹窗 -->
    <Modal v-model="showImport" title="导入租客（CSV 模板）">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground leading-relaxed">
          下载模板填写后上传（.csv，≤2MB）。房源需先在「房源管理」建档（可先用房源导入批量建档）；同房源同名在租租户按“非空覆盖、空值保留”更新，不会重复登记。
        </p>
        <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="downloadTenantTemplate">⬇ 下载导入模板</button>
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

    <!-- 租户资料粘贴识别：租客微信发资料 → 粘贴解析成草稿 → 核对后批量入库 -->
    <TenantPasteModal v-model="showPaste" :communities="communityOptions" @imported="load(1)" />

    <!-- 关联登录账号：租客自己注册后，房东在这里把它绑到该租户档案上 -->
    <TenantAccountModal
      ref="accountModalRef"
      v-model="showAccount"
      :tenant="accountTenant"
      @changed="onAccountChanged"
      @request-unbind="askAccountUnbind"
    />

    <!-- 租户提交的资料变更申请：逐项对照后通过（覆盖正式资料）或驳回 -->
    <TenantChangeReviewModal v-model="showChangeReview" @changed="onChangeReviewed" />

    <!-- 住户成员：挂在租户名下的同住人，只登记身份、不参与计费（docs/08） -->
    <TenantMembersModal v-model="showMembers" :tenant="membersTenant" @changed="load(page)" />

    <!-- 续租：带历史留痕（续了几次/每次多长/租金怎么变），不走编辑表单覆盖日期 -->
    <RenewModal
      v-model="showRenew"
      :tenant="renewTenantRow"
      :room-label="renewTenantRow?.room_label || renewTenantRow?.room_no || ''"
      :current-rent="renewRoomRent"
      @renewed="onRenewed"
    />

    <!-- 合同打印：模板合同（租赁合同/续租协议）+ 已上传件按原文件打印 -->
    <ContractPrintModal
      v-model="showContractPrint"
      :tenant="contractPrintTenant"
      :room="contractPrintRoom"
    />

    <ConfirmDialog
      v-model="confirmState.show"
      :title="confirmState.title"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="onConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useLedgerStore } from '../stores/ledger'
import { ID_CARD_PATTERN, idCardHint } from '../utils/id-card'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DateField from '../components/DateField.vue'
import FilePick from '../components/FilePick.vue'
import FilterPanel from '../components/FilterPanel.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import TagInput from '../components/TagInput.vue'
import TenantAccountModal from '../components/TenantAccountModal.vue'
import TenantChangeReviewModal from '../components/TenantChangeReviewModal.vue'
import TenantDepositPhotos from '../components/TenantDepositPhotos.vue'
import TenantMembersModal from '../components/TenantMembersModal.vue'
import TenantPasteModal from '../components/TenantPasteModal.vue'
import LeaseTermChips from '../components/LeaseTermChips.vue'
import ContractPrintModal from '../components/ContractPrintModal.vue'
import RenewModal from '../components/RenewModal.vue'
import { getChangeRequestPendingCount } from '../api/tenant'
import { toast } from '../utils/toast'
import {
  checkoutTenant, createTenant, deleteAttachment, deleteContract, deleteTenant, downloadAttachment,
  downloadContract, downloadTenantTemplate, exportTenants, fetchAttachmentBlob, fetchContractBlob, getAttachments,
  getContracts, getFeeItems, getAllRooms, getAllTenants, getTenants, getTenantApplications, getTenantMembers, fetchApplicationPhoto,
  rejectTenantApplication, importTenants, updateTenant, uploadAttachments, uploadContracts,
  type Contract, type FeeItem, type ImportResult, type RoomView, type Tenant, type TenantApplication,
  type TenantAttachment, type TenantMember, type TenantMemberPayload,
} from '../api/rental'
import {
  defaultBilling, fetchBillingDefaults, globalBillingText, payDayText,
  tenantBillingSummary, waterModeLabel, type BillingDefaults, type WaterMode,
} from '../utils/billing'
import { cycleLabel, payCycleOptions } from '../utils/cycle'
import { leaseEndDateStr } from '../utils/lease'

const ledgerStore = useLedgerStore()

const tenants = ref<Tenant[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const keyword = ref('')
const active = ref('')
const roomId = ref(0)
const community = ref('')
const filterTag = ref('')

// 手机端筛选摘要行与非默认条件数（徽标）
const tenantsFilterSummary = computed(() =>
  [
    keyword.value ? `“${keyword.value}”` : '未搜索',
    active.value === '1' ? '在租' : active.value === '0' ? '已退租' : '全部状态',
    community.value || '全部小区',
    roomId.value ? (allRooms.value.find((r) => r.id === roomId.value)?.room_no || '指定房源') : '全部房源',
    filterTag.value ? `#${filterTag.value}` : '全部标签',
  ].join(' · '),
)
const tenantsFilterCount = computed(() => (keyword.value ? 1 : 0) + (active.value ? 1 : 0) + (community.value ? 1 : 0) + (roomId.value ? 1 : 0) + (filterTag.value ? 1 : 0))

const filterRef = ref<InstanceType<typeof FilterPanel> | null>(null)

function applyFilters() {
  load(1)
  filterRef.value?.collapse()
}

// 导出租户 CSV（派出所场景）：筛选参数与 load() 用的**逐个相同**，做到
// 「列表看到什么，导出就是什么」；导出的是全部匹配行（不受分页限制）。
// 后端列 = 租客模板 15 列 + 末尾只读 4 列（房号/在租状态/出生日期/性别）。
function exportList() {
  exportTenants({
    keyword: keyword.value,
    active: active.value,
    room_id: roomId.value || undefined,
    community: community.value || undefined,
    tag: filterTag.value || undefined,
  })
}

// 全局计费与缴费默认（偏好设置 → 租房设置）：租户表单的预填值与"跟随全局"提示
const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })

const showModal = ref(false)
const editing = ref<Tenant | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref<Partial<Tenant>>({})
// 新租户表单里暂存的押金单照片：保存成功拿到租户 ID 后再上传（编辑态组件自己即时上传）
const depositFiles = ref<File[]>([])

// 租期快捷 chip 的选中态：0 = 手填到期日（不选中任何时长）。打开表单或手改日期时
// 都按「入住日期 + 到期日」反推一次，保证 chip 高亮与真实日期一致，不会出现
// 「看起来选了半年、日期却是手填的三个月」这种自相矛盾。
const formLeaseTerm = ref(0)

/** 由入住日期与到期日反推最接近的整月租期；对不上任何一个预置项就返回 0。 */
function inferLeaseTerm(moveIn?: string, leaseEnd?: string): number {
  if (!moveIn || !leaseEnd) return 0
  for (const opt of [1, 3, 6, 12]) {
    if (leaseEndDateStr(moveIn, opt) === leaseEnd) return opt
  }
  // 12 个月以上的整数年也认（两年涨租合同）。
  for (let y = 2; y <= 5; y++) {
    if (leaseEndDateStr(moveIn, y * 12) === leaseEnd) return y * 12
  }
  return 0
}

/** chip 被点击：0 表示清除（恢复手填），否则把算好的到期日写进表单。 */
function onTenantFormTerm(months: number) {
  formLeaseTerm.value = months
  if (months > 0) form.value.lease_end_date = leaseEndDateStr(form.value.move_in_date || '', months)
}

// 入住日期或到期日一变（含用户点日历手改），重新反推 chip 高亮。
watch(
  () => [form.value.move_in_date, form.value.lease_end_date],
  ([mi, le]) => { formLeaseTerm.value = inferLeaseTerm(mi as string, le as string) },
)

// ---------- 批量导入 ----------

const showImport = ref(false)
const importing = ref(false)
const importError = ref('')
const importResult = ref<ImportResult | null>(null)
const importFile = ref<File | null>(null)
const importFileName = computed(() => importFile.value?.name || '')

// ---------- 粘贴识别（租客微信发资料 → 解析草稿 → 核对后入库） ----------

const showPaste = ref(false)

function onPickImportFile(files: File[]) {
  importFile.value = files[0] ?? null
  importResult.value = null
  importError.value = ''
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
    const res = await importTenants(file)
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

// ---------- 收费项目勾选 ----------

// 收费项目定义（内置六项 + 用户自定义），表单勾选与"不含 X"摘要共用
const feeItems = ref<FeeItem[]>([])
// 表单里未勾选（不参与计费）的项目 key，与接口的 excluded_fees 同口径
const excludedInput = ref<string[]>([])

// 参与勾选的项目：启用的内置 + 自定义（停用项目本就不出账，不展示）
const billableFeeItems = computed(() => feeItems.value.filter((f) => f.enabled))

function feeChecked(key: string): boolean {
  return !excludedInput.value.includes(key)
}

function toggleFee(key: string) {
  const set = new Set(excludedInput.value)
  if (set.has(key)) set.delete(key)
  else set.add(key)
  excludedInput.value = [...set]
}

function feeName(key: string): string {
  return feeItems.value.find((f) => f.key === key)?.name ?? key
}

// "不参与：X、Y"摘要文案（只列当前可勾选的项目，停用/已删项目不出账无需提示）
const excludedFeeNames = computed(() =>
  excludedInput.value.filter((k) => billableFeeItems.value.some((f) => f.key === k)).map(feeName).join('、'),
)

// 租户列表摘要后缀：不参与计费的项目名（空=全项参与不打标）
function tenantExcludedText(t: Tenant): string {
  const names = (t.excluded_fees || [])
    .filter((k) => billableFeeItems.value.some((f) => f.key === k))
    .map(feeName)
    .join('、')
  return names ? `（不含${names}）` : ''
}

// 表单当前生效的水费计费方式：租户显式选择则用它，'' 表示跟随全局默认
const formWaterMode = computed<WaterMode>(() => {
  const m = form.value.water_mode
  if (m === 'monthly' || m === 'meter') return m
  return billingDefaults.value.waterMode
})

// 是否跟随全局默认：未显式选择计费方式（金额输入框只读展示全局值）
const formWaterFollows = computed(() => {
  const m = form.value.water_mode
  return m !== 'monthly' && m !== 'meter'
})

// 跟随全局时展示的全局金额（按当前生效计费方式）
const formWaterGlobalAmount = computed(() => formWaterMode.value === 'monthly'
  ? billingDefaults.value.waterMonthlyFee
  : billingDefaults.value.waterMeterPrice)

// 缴费日 / 提前提醒天数：-1 表示跟随全局默认，输入框留空展示
const payDayInput = computed<string | number>({
  get: () => {
    const v = Number(form.value.pay_day)
    return Number.isFinite(v) && v >= 0 ? v : ''
  },
  set: (v) => { form.value.pay_day = v === '' || v === null || v === undefined ? -1 : Number(v) },
})

const remindDaysInput = computed<string | number>({
  get: () => {
    const v = Number(form.value.remind_days)
    return Number.isFinite(v) && v >= 0 ? v : ''
  },
  set: (v) => { form.value.remind_days = v === '' || v === null || v === undefined ? -1 : Number(v) },
})

// 切到显式计费方式时，金额留空则按全局默认预填（用户可改，改回 0 仍跟随全局）
function onWaterModeChange() {
  const m = form.value.water_mode
  if (m === 'monthly' && !(Number(form.value.water_monthly_fee) > 0)) {
    form.value.water_monthly_fee = billingDefaults.value.waterMonthlyFee
  }
  if (m === 'meter' && !(Number(form.value.water_price) > 0)) {
    form.value.water_price = billingDefaults.value.waterMeterPrice
  }
}

function openCreate() {
  editing.value = null
  convertingId.value = 0
  depositFiles.value = []
  form.value = {
    name: '', phone: '', id_card: '', room_id: allRooms.value[0]?.id,
    address: '', emergency_contact: '', emergency_phone: '',
    move_in_date: '', lease_end_date: '', deposit: 0,
    // 空串/-1 = 跟随全局默认；金额按全局默认预填，切到显式方式时直接可用
    water_mode: '', water_price: billingDefaults.value.waterMeterPrice,
    water_monthly_fee: billingDefaults.value.waterMonthlyFee,
    pay_cycle: '', pay_day: -1, remind_days: -1,
    tags: [],
    notes: '',
  }
  formLeaseTerm.value = 0
  // 新租户默认全部收费项目参与
  excludedInput.value = []
  formMembers.value = []
  formError.value = ''
  showModal.value = true
}

// ---------- 表单里的「入住成员」 ----------
// 与租户一起保存：带 id 的改、不带 id 的新增、删掉的行保存后即移除。
// uid 只用于 v-for 的 key（新行还没有 id），提交前会剥掉。
type MemberRow = TenantMemberPayload & { uid: number }
const MEMBER_RELATIONS = ['配偶', '子女', '父母', '兄弟姐妹', '亲属', '同事', '朋友', '其他']
const formMembers = ref<MemberRow[]>([])
let memberUID = 0

function addMemberRow() {
  formMembers.value.push({
    uid: ++memberUID,
    name: '', relation: '', phone: '', id_card: '',
    move_in_date: '', move_out_date: '', active: true, notes: '',
  })
}

function removeMemberRow(index: number) {
  formMembers.value.splice(index, 1)
}

// 加载已有成员（编辑态）：把库里的行映射成表单行，保留 id 以便原地更新
async function loadFormMembers(tenantId: number) {
  formMembers.value = []
  try {
    const res = await getTenantMembers({ tenant_id: tenantId })
    if (res.data?.code !== 0) return
    formMembers.value = (res.data.data ?? []).map((m: TenantMember) => ({
      uid: ++memberUID,
      id: m.id,
      name: m.name, relation: m.relation, phone: m.phone, id_card: m.id_card,
      move_in_date: m.move_in_date, move_out_date: m.move_out_date,
      active: m.active, notes: m.notes,
    }))
  } catch {
    // 拉不到就按「本次不动成员」处理（保存时 members 字段仍会带上空数组，
    // 但下面 save 会带上已加载的部分；失败时提示用户重开弹窗）
    formError.value = '入住成员加载失败，请关闭弹窗重试'
  }
}

// 表单标签的 v-model 桥：form.tags 在 Partial<Tenant> 里可缺省，TagInput 需要确定的数组
const formTags = computed<string[]>({
  get: () => form.value.tags ?? [],
  set: (v) => { form.value.tags = v },
})

// 房源完整位置：小区 楼栋 单元 N层 房号
function roomLabelOf(r: RoomView): string {
  const parts = [r.community, r.building, r.unit, r.floor ? r.floor + '层' : '', r.room_no]
  return parts.filter(Boolean).join(' ')
}

// ------- 入住申请审核（租客自助登记） -------

const showApplications = ref(false)
const applications = ref<TenantApplication[]>([])
const applicationsLoading = ref(false)
const photoUrls = ref<Record<number, string>>({})
// 待审核申请数：既要支撑弹窗里的徽标，也要在**页面加载时**就显示红点。
// 此前只由 applications 派生，而 applications 只在点开弹窗时才拉，页面首屏
// 永远没有角标（2026-10-11 审计）。这里改成独立计数：挂载时轻量拉一次
// （status=pending），打开弹窗/处理完申请后再刷新。
const pendingCount = ref(0)
async function loadPendingCount() {
  try {
    const res = await getTenantApplications('pending')
    const rows = res.data?.data?.items || res.data?.data || []
    pendingCount.value = Array.isArray(rows) ? rows.length : 0
  } catch {
    /* 角标失败不影响页面 */
  }
}

// ---------- 租户登录账号关联（v0.6.10） ----------

const showAccount = ref(false)
const accountTenant = ref<Tenant | null>(null)
const accountModalRef = ref<InstanceType<typeof TenantAccountModal> | null>(null)

function openAccount(t: Tenant) {
  accountTenant.value = t
  showAccount.value = true
}

function onAccountChanged() {
  // 关联状态会显示在租户列表的「账号✓」上，刷新列表即可。
  void load(page.value)
}

// 解绑确认交给页面的 ConfirmDialog（子组件不内嵌确认，避免叠两层弹窗）。
function askAccountUnbind() {
  const t = accountTenant.value
  if (!t) return
  showAccount.value = false
  askConfirm(
    '解除账号关联',
    `确定解除「${t.name}」的登录账号关联？该账号会回到「等待房东关联」状态（账号不删除），可再关联到别的租户。`,
    async () => {
      await accountModalRef.value?.doUnbind()
      showAccount.value = true
    },
  )
}

// ---------- 资料变更申请（v0.6.10） ----------

const showChangeReview = ref(false)
const changePendingCount = ref(0)

async function loadChangePendingCount() {
  try {
    const res = await getChangeRequestPendingCount()
    if (res.data?.code === 0) {
      changePendingCount.value = Number(res.data.data?.count || 0)
    }
  } catch {
    /* 角标失败不影响页面 */
  }
}

function onChangeReviewed() {
  void loadChangePendingCount()
}
// 正在转入登记的申请 id：保存租户时带给服务端（标记已入库 + 照片转附件）
const convertingId = ref(0)

function openApplications() {
  showApplications.value = true
  loadApplications()
}

async function loadApplications() {
  applicationsLoading.value = true
  try {
    const res = await getTenantApplications()
    if (res.data?.code === 0) {
      applications.value = res.data.data ?? []
      pendingCount.value = applications.value.filter((a) => a.status === 'pending').length
      // 已不在列表里的照片 objectURL 顺手释放
      for (const id of Object.keys(photoUrls.value)) {
        if (!applications.value.some((a) => a.id === Number(id))) {
          URL.revokeObjectURL(photoUrls.value[Number(id)])
          delete photoUrls.value[Number(id)]
        }
      }
    }
  } finally {
    applicationsLoading.value = false
  }
}

async function loadApplicationPhoto(a: TenantApplication) {
  try {
    photoUrls.value[a.id] = await fetchApplicationPhoto(a.id)
  } catch {
    toast('照片加载失败', 'error')
  }
}

function fmtAppTime(s: string): string {
  return (s || '').slice(5, 16).replace('T', ' ')
}

// 转入登记：带出申请资料，预选房源，走现有登记租户弹窗（计费设置照常配置）
function convertToTenant(a: TenantApplication) {
  showApplications.value = false
  editing.value = null
  convertingId.value = a.id
  form.value = {
    name: a.name, phone: a.phone, id_card: a.id_card,
    // 身份证住址原先只存在申请里、转入登记后就丢了（v0.6.10 补上正式档案字段）。
    address: a.address || '', emergency_contact: '', emergency_phone: '',
    room_id: a.room_id || allRooms.value[0]?.id,
    move_in_date: '', lease_end_date: '', deposit: 0,
    water_mode: '', water_price: billingDefaults.value.waterMeterPrice,
    water_monthly_fee: billingDefaults.value.waterMonthlyFee,
    pay_cycle: '', pay_day: -1, remind_days: -1,
    tags: [], notes: '',
  }
  formLeaseTerm.value = 0
  excludedInput.value = []
  formError.value = ''
  showModal.value = true
}

function rejectApplication(a: TenantApplication) {
  askConfirm('拒绝申请', `拒绝 ${a.name}（${a.phone}）的入住申请？其身份证照片会一并删除。`, async () => {
    const res = await rejectTenantApplication(a.id, '')
    if (res.data?.code === 0) {
      toast('已拒绝')
      await loadApplications()
    } else {
      toast(res.data?.message || '操作失败', 'error')
    }
  })
}

// 押金显示：带千分位，0 不显示（在列表里以 — 呈现）
function fmtDeposit(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// 租约到期状态：已过期红 / 30 天内到期橙；未约定或非在租不提示
function leaseStatus(t: Tenant): { text: string; cls: string } | null {
  if (!t.active || !t.lease_end_date) return null
  const days = Math.ceil((new Date(t.lease_end_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
  if (days < 0) return { text: `已过期 ${-days} 天`, cls: 'text-rose-500' }
  if (days <= 30) return { text: `${days} 天后到期`, cls: 'text-amber-500' }
  return null
}

function openEdit(t: Tenant) {
  editing.value = t
  depositFiles.value = []
  form.value = { ...t }
  formLeaseTerm.value = inferLeaseTerm(t.move_in_date, t.lease_end_date)
  excludedInput.value = [...(t.excluded_fees || [])]
  formMembers.value = []
  formError.value = ''
  showModal.value = true
  void loadFormMembers(t.id)
}

async function loadRooms() {
  // 跨页拉全（服务端 pageSize 上限 100，写死会让多房源的小区从下拉里消失）。
  allRooms.value = await getAllRooms()
}

// 小区/村组下拉候选；房源下拉按所选小区联动收窄
const communityOptions = computed(() => [...new Set(allRooms.value.map((r) => r.community).filter(Boolean))])
const roomOptions = computed(() =>
  community.value ? allRooms.value.filter((r) => r.community === community.value) : allRooms.value,
)

// 标签筛选候选 + 表单打标建议：跨页拉全所有租户的已用标签（此前只取前 100 个，
// 租户多了以后新标签在筛选下拉里看不到）。
const tagOptions = ref<string[]>([])
async function loadTagOptions() {
  const rows = await getAllTenants()
  tagOptions.value = [...new Set<string>(rows.flatMap((t: Tenant) => t.tags ?? []))].sort()
}

function onCommunityChange() {
  // 已选房源不在新小区里时清掉，避免「小区 + 别的小区的房」矛盾组合
  if (roomId.value && !roomOptions.value.some((r) => r.id === roomId.value)) roomId.value = 0
  load(1)
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getTenants({
      page: p, pageSize, keyword: keyword.value,
      active: active.value, room_id: roomId.value || undefined,
      community: community.value || undefined,
      tag: filterTag.value || undefined,
    })
    if (res.data?.code === 0) {
      tenants.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.name?.trim() || !form.value.room_id) {
    formError.value = '请填写姓名并选择房源'
    return
  }
  // 入住成员的行内校验：逐行报第几位，避免只丢一句「参数错误」
  for (let i = 0; i < formMembers.value.length; i++) {
    const m = formMembers.value[i]
    if (!m.name?.trim()) {
      formError.value = `入住成员第 ${i + 1} 位还没填姓名`
      return
    }
    const idCard = (m.id_card || '').trim().toUpperCase()
    if (idCard && !ID_CARD_PATTERN.test(idCard)) {
      formError.value = `入住成员第 ${i + 1} 位的身份证号应为 18 位（末位可为 X）或 15 位`
      return
    }
  }
  saving.value = true
  formError.value = ''
  try {
    // 只有与所选计费方式匹配的金额才落库；跟随全局默认（方式为空）时两个金额都存 0，
    // 这样以后改偏好设置里的全局默认，租户会跟着变。
    const mode = form.value.water_mode
    // 未勾选的收费项目（排除项）落库；存量排除里已停用/已删除的项目一并清掉。
    const excluded = billableFeeItems.value.filter((f) => !feeChecked(f.key)).map((f) => f.key)
    const payload: Partial<Tenant> & { application_id?: number; members?: TenantMemberPayload[] } = {
      ...form.value,
      water_price: mode === 'meter' ? Number(form.value.water_price) || 0 : 0,
      water_monthly_fee: mode === 'monthly' ? Number(form.value.water_monthly_fee) || 0 : 0,
      pay_day: Number(form.value.pay_day ?? -1),
      remind_days: Number(form.value.remind_days ?? -1),
      excluded_fees: excluded,
      // 租客自助登记转入：带上申请 id，服务端标记已入库并把身份证照片转进附件
      application_id: editing.value ? undefined : convertingId.value || undefined,
      // 入住成员与租户一起保存：uid 只是界面 key，提交给服务端前剥掉；
      // 各行的搬离日期填了就按已搬离存，免得出现「已搬离却还在住」的矛盾记录。
      members: formMembers.value.map(({ uid: _uid, ...row }) => ({
        ...row,
        name: (row.name || '').trim(),
        relation: (row.relation || '').trim(),
        phone: (row.phone || '').trim(),
        id_card: (row.id_card || '').trim().toUpperCase(),
        active: row.move_out_date ? false : row.active !== false,
      })),
    }
    const res = editing.value
      ? await updateTenant(editing.value.id, payload)
      : await createTenant(payload)
    if (res.data?.code === 0) {
      // 新租户表单里暂存的押金单照片：拿到新建租户 ID 后补传（编辑态组件已即时上传）。
      const createdId = Number((res.data as { data?: { id?: number } })?.data?.id || 0)
      const pendingDeposit = depositFiles.value
      depositFiles.value = []
      showModal.value = false
      await load(1)
      // 标签候选取自租户列表，打完新标签要重取，筛选下拉才能立刻选到
      loadTagOptions()
      if (!editing.value && createdId > 0 && pendingDeposit.length > 0) {
        try {
          const up = await uploadAttachments(createdId, pendingDeposit, '押金单')
          if (up.data?.code === 0) {
            toast(`已上传 ${pendingDeposit.length} 张押金单照片`)
            await load(1)
          } else {
            toast(up.data?.message || '押金单照片上传失败，可在租户列表点「押金单」补传', 'error')
          }
        } catch {
          toast('租户已登记，但押金单照片上传失败，可在租户列表点「押金单」补传', 'error')
        }
      }
      // 转入登记完成：刷新申请队列（徽标数同步减少）
      if (convertingId.value) {
        convertingId.value = 0
        loadApplications()
      }
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch {
    formError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, title: '', message: '', action: null as null | (() => Promise<void>) })

function askConfirm(title: string, message: string, action: () => Promise<void>) {
  confirmState.value = { show: true, title, message, action }
}

async function onConfirm() {
  await confirmState.value.action?.()
}

function checkout(t: Tenant) {
  askConfirm('办理退租', `确认为租户「${t.name}」办理退租？历史账单不受影响。`, async () => {
    const res = await checkoutTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '退租失败', 'error')
      return
    }
    toast('已办理退租')
    await load(page.value)
  })
}

function remove(t: Tenant) {
  askConfirm('删除租户', `确定删除租户「${t.name}」的登记信息？`, async () => {
    const res = await deleteTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    await load(page.value)
  })
}

// ---------- 合同存档 ----------

const showContracts = ref(false)
const contractTenant = ref<Tenant | null>(null)
const contracts = ref<Contract[]>([])
const contractLoading = ref(false)
const contractUploading = ref(false)
const contractError = ref('')
const previewing = ref<Contract | null>(null)
const previewUrl = ref('')

const contractTenantLabel = computed(() => {
  const t = contractTenant.value
  if (!t) return ''
  return `${t.name} · ${t.room_label || t.room_no || ''}`
})

function openContracts(t: Tenant) {
  contractTenant.value = t
  contracts.value = []
  contractError.value = ''
  showContracts.value = true
  loadContracts(t.id)
}

async function loadContracts(tenantId: number) {
  contractLoading.value = true
  try {
    const res = await getContracts(tenantId)
    if (res.data?.code === 0) contracts.value = res.data.data ?? []
  } catch {
    contractError.value = '加载合同失败'
  } finally {
    contractLoading.value = false
  }
}

// ---------- 合同打印 ----------
// 打印入口挂在合同弹窗里（用户看合同的地方），但打印是独立弹窗：模板合同要预览
// A4 纸张、已上传件要按原文件打印，混在存档弹窗里会让两个弹窗互相遮挡。
const showContractPrint = ref(false)
const contractPrintTenant = ref<Tenant | null>(null)
const contractPrintRoom = computed(() => {
  const rid = contractPrintTenant.value?.room_id
  return rid ? allRooms.value.find((r) => r.id === rid) ?? null : null
})

function openContractPrint() {
  contractPrintTenant.value = contractTenant.value
  showContractPrint.value = true
}

async function uploadContractFiles(files: File[]) {
  const t = contractTenant.value
  if (!t || files.length === 0) return
  contractUploading.value = true
  contractError.value = ''
  try {
    const res = await uploadContracts(t.id, files)
    if (res.data?.code === 0) {
      toast('合同已上传')
      await loadContracts(t.id)
      await load(page.value)
    } else {
      contractError.value = res.data?.message || '上传失败'
    }
  } catch (err: any) {
    contractError.value = err.response?.data?.message || '上传失败，请重试'
  } finally {
    contractUploading.value = false
  }
}

function previewContract(ct: Contract) {
  contractError.value = ''
  fetchContractBlob(ct.id)
    .then((blob) => {
      releasePreviewUrl()
      previewing.value = ct
      previewUrl.value = URL.createObjectURL(blob)
    })
    .catch(() => toast('预览失败，请重试', 'error'))
}

function closePreview() {
  releasePreviewUrl()
  previewing.value = null
}

function releasePreviewUrl() {
  if (previewUrl.value) {
    URL.revokeObjectURL(previewUrl.value)
    previewUrl.value = ''
  }
}

function closeContracts() {
  closePreview()
  showContracts.value = false
}

async function download(ct: Contract) {
  try {
    await downloadContract(ct.id, ct.file_name)
  } catch {
    toast('下载失败，请重试', 'error')
  }
}

function removeContract(ct: Contract) {
  askConfirm('删除合同', `确定删除「${ct.file_name}」？删除后不可恢复。`, async () => {
    const res = await deleteContract(ct.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    if (contractTenant.value) await loadContracts(contractTenant.value.id)
    await load(page.value)
  })
}

// ---------- 住户成员 ----------
// 与费用无关的同住人：只登记身份信息（见 docs/08-租户与住户成员设计.md）。
const showMembers = ref(false)
const membersTenant = ref<Tenant | null>(null)

// ---------- 续租（2026-10-20 粉丝需求） ----------
// 行数据里已有 room_id，补上房源默认月租给弹窗做「涨/降租金」对比：
// 租户表不存租金，账单一直是「房源默认月租 × 月数」（bills.go），所以续租调租金
// 调的就是这个字段。
const showRenew = ref(false)
const renewTenantRow = ref<Tenant | null>(null)
const renewRoomRent = computed(() => {
  const rid = renewTenantRow.value?.room_id
  return allRooms.value.find((r) => r.id === rid)?.default_rent ?? 0
})

function openRenew(t: Tenant) {
  renewTenantRow.value = t
  showRenew.value = true
}

/** 续租成功：重拉列表（到期日/续租次数/租金都可能变）。 */
async function onRenewed() {
  await load(page.value)
  // 房源默认月租可能被改（续租时调租金），顺带刷新房源缓存。
  await loadRooms()
}

function openMembers(t: Tenant) {
  membersTenant.value = t
  showMembers.value = true
}

// ---------- 押金单照片 ----------
// 押金不能只录一个数字：实物押金单拍照留档（复用附件能力，标签=押金单），
// 列表的押金列与编辑表单里都有入口。
const showDepositPhotos = ref(false)
const depositTenant = ref<Tenant | null>(null)

function openDepositPhotos(t: Tenant) {
  depositTenant.value = t
  showDepositPhotos.value = true
}

function onDepositPhotosChanged() {
  // 张数变了，刷新列表上的「押金单 n」
  void load(page.value)
}

// ---------- 租户资料附件 ----------
// 与合同存档分开的另一套留档：租房协议、汽车车牌、租客本人照片、身份证等。
const showAttachments = ref(false)
const attachmentTenant = ref<Tenant | null>(null)
const attachments = ref<TenantAttachment[]>([])
const attachmentLoading = ref(false)
const attachmentUploading = ref(false)
const attachmentError = ref('')
const attachmentLabel = ref('')
const attachmentPreviewing = ref<TenantAttachment | null>(null)
const attachmentPreviewUrl = ref('')

// 常见用途，点选比手打快；也允许不选（留空）。
// 「押金单」同时是押金单照片专用入口的过滤标签（见 TenantDepositPhotos）。
const ATTACHMENT_LABELS = ['身份证', '汽车车牌', '租房协议', '租客照片', '押金单', '其他']

const attachmentTenantLabel = computed(() => {
  const t = attachmentTenant.value
  if (!t) return ''
  return `${t.name} · ${t.room_label || t.room_no || ''}`
})

function openAttachments(t: Tenant) {
  attachmentTenant.value = t
  attachments.value = []
  attachmentError.value = ''
  attachmentLabel.value = ''
  showAttachments.value = true
  loadAttachments(t.id)
}

async function loadAttachments(tenantId: number) {
  attachmentLoading.value = true
  try {
    const res = await getAttachments(tenantId)
    if (res.data?.code === 0) attachments.value = res.data.data ?? []
  } catch {
    attachmentError.value = '加载附件失败'
  } finally {
    attachmentLoading.value = false
  }
}

async function uploadAttachmentFiles(files: File[]) {
  const t = attachmentTenant.value
  if (!t || files.length === 0) return
  attachmentUploading.value = true
  attachmentError.value = ''
  try {
    const res = await uploadAttachments(t.id, files, attachmentLabel.value)
    if (res.data?.code === 0) {
      const n = res.data.data?.uploaded ?? files.length
      toast(`已上传 ${n} 个附件`)
      await loadAttachments(t.id)
    } else {
      attachmentError.value = res.data?.message || '上传失败'
    }
  } catch (err: any) {
    attachmentError.value = err.response?.data?.message || '上传失败，请重试'
  } finally {
    attachmentUploading.value = false
  }
}

function previewAttachment(at: TenantAttachment) {
  attachmentError.value = ''
  fetchAttachmentBlob(at.id)
    .then((blob) => {
      releaseAttachmentPreviewUrl()
      attachmentPreviewing.value = at
      attachmentPreviewUrl.value = URL.createObjectURL(blob)
    })
    .catch(() => toast('预览失败，请重试', 'error'))
}

function closeAttachmentPreview() {
  releaseAttachmentPreviewUrl()
  attachmentPreviewing.value = null
}

function releaseAttachmentPreviewUrl() {
  if (attachmentPreviewUrl.value) {
    URL.revokeObjectURL(attachmentPreviewUrl.value)
    attachmentPreviewUrl.value = ''
  }
}

function closeAttachments() {
  closeAttachmentPreview()
  showAttachments.value = false
}

async function downloadAttachmentFile(at: TenantAttachment) {
  try {
    await downloadAttachment(at.id, at.file_name)
  } catch {
    toast('下载失败，请重试', 'error')
  }
}

function removeAttachment(at: TenantAttachment) {
  askConfirm('删除附件', `确定删除「${at.file_name}」？删除后不可恢复。`, async () => {
    const res = await deleteAttachment(at.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    if (attachmentTenant.value) await loadAttachments(attachmentTenant.value.id)
  })
}

function fmtSize(v: number): string {
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MB`
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`
  return `${v} B`
}
function fmtDate(v: string): string {
  return (v || '').slice(0, 10)
}

async function loadFeeItems() {
  try {
    const res = await getFeeItems()
    if (res.data?.code === 0) feeItems.value = res.data.data ?? []
  } catch {
    feeItems.value = [] // 加载失败时表单按"全项参与"处理
  }
}

onMounted(async () => {
  billingDefaults.value = await fetchBillingDefaults()
  await Promise.all([loadRooms(), loadFeeItems()])
  loadTagOptions()
  void loadPendingCount()
  void loadChangePendingCount()
  await load(1)
})
</script>
