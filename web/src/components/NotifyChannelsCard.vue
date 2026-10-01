<template>
  <div class="surface rounded-2xl p-4 sm:p-6">
    <div class="flex items-start justify-between gap-3 mb-4">
      <div>
        <h3 class="text-sm sm:text-base font-bold text-foreground">缴费提醒通知</h3>
        <p class="text-xs text-muted-foreground mt-1 leading-relaxed">
          每天早上 8 点汇总发送一次临期与逾期的缴费提醒（需先在「房源管理」给房间设置缴费日）。
          绑定目标加密存储，可随时停用或解绑。
        </p>
      </div>
      <button class="btn-ghost !py-1.5 shrink-0" @click="load" :disabled="loading">{{ loading ? '刷新中…' : '刷新' }}</button>
    </div>

    <div class="space-y-3">
      <div v-for="ch in channels" :key="ch.channel" class="rounded-xl border border-border p-3.5 space-y-3">
        <!-- 渠道卡片头：名称 / 状态 / 操作 -->
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-bold text-foreground">{{ ch.label }}</span>
              <span class="badge !px-1.5 !py-0.5 text-[11px]" :class="statusClass(ch)">{{ statusText(ch) }}</span>
            </div>
            <p class="text-xs text-muted-foreground mt-1">{{ ch.description }}</p>
            <p v-if="ch.channel === 'email' && ch.bindings?.length" class="text-xs mt-1 text-muted-foreground">
              已绑 {{ ch.bindings.length }} 个邮箱：{{ ch.bindings.map((b) => b.target_masked).join('、') }}
            </p>
            <p v-else-if="ch.bound" class="text-xs mt-1 text-muted-foreground">{{ ch.target_masked }}</p>
            <p v-if="ch.bot_link" class="text-xs mt-0.5">
              <a :href="ch.bot_link" target="_blank" rel="noopener" class="text-brand-600 dark:text-brand-300 hover:underline">打开 QQ 机器人主页</a>
            </p>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <label
              v-if="ch.bound"
              class="inline-flex items-center cursor-pointer"
              :title="ch.status === 'active' ? '点击停用' : '点击启用'"
            >
              <input
                type="checkbox"
                class="sr-only"
                :checked="ch.status === 'active'"
                @change="toggle(ch)"
              />
              <span
                class="relative w-9 h-5 rounded-full transition-colors"
                :class="ch.status === 'active' ? 'bg-emerald-500' : 'bg-muted-foreground/30'"
              >
                <span
                  class="absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform"
                  :class="ch.status === 'active' ? 'translate-x-4' : ''"
                ></span>
              </span>
            </label>
            <button
              v-if="ch.bound && ch.status === 'active'"
              class="btn-ghost !py-1 !px-2.5 text-xs"
              :disabled="busy"
              @click="test(ch)"
            >测试</button>
            <button
              class="btn-ghost !py-1 !px-2.5 text-xs"
              @click="expanded = expanded === ch.channel ? '' : ch.channel"
            >{{ expandText(ch) }}</button>
          </div>
        </div>

        <!-- 展开区：按渠道渲染配置/绑定表单 -->
        <div v-if="expanded === ch.channel" class="border-t border-border/60 pt-3 space-y-3">
          <!-- 钉钉：直接粘 Webhook -->
          <template v-if="ch.channel === 'dingtalk'">
            <div class="flex flex-col sm:flex-row gap-2">
              <input
                v-model="bindTarget.dingtalk"
                class="input-field !py-2 flex-1"
                placeholder="https://oapi.dingtalk.com/robot/send?access_token=…"
              />
              <button class="btn-brand !py-2 shrink-0" :disabled="busy" @click="bind('dingtalk')">保存 Webhook</button>
            </div>
            <p class="text-[11px] text-muted-foreground">
              在钉钉群「设置 → 群机器人 → 添加机器人 → 自定义」创建，安全设置选「自定义关键词」并填 <strong>提醒</strong> 两个字。
            </p>
          </template>

          <!-- 邮箱：SMTP 配置 + 多邮箱绑定 -->
          <template v-else-if="ch.channel === 'email'">
            <div class="grid grid-cols-2 sm:grid-cols-3 gap-2">
              <label class="block col-span-2 sm:col-span-1">
                <span class="text-[11px] text-muted-foreground block mb-1">SMTP 服务器</span>
                <input v-model="emailForm.host" class="input-field !py-1.5" placeholder="smtp.qq.com" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">端口（587/465）</span>
                <input v-model="emailForm.port" class="input-field !py-1.5" placeholder="587" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">发件邮箱</span>
                <input v-model="emailForm.from_address" class="input-field !py-1.5" placeholder="you@qq.com" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">发件人名称</span>
                <input v-model="emailForm.from_name" class="input-field !py-1.5" placeholder="租房管理" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">SMTP 用户名</span>
                <input v-model="emailForm.username" class="input-field !py-1.5" placeholder="一般与发件邮箱相同" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">授权码 / 密码</span>
                <input v-model="emailForm.password" type="password" class="input-field !py-1.5" placeholder="已配置则留空不改" />
              </label>
            </div>
            <button class="btn-ghost !py-1.5" :disabled="busy" @click="saveProvider('email')">保存 SMTP 配置</button>
            <div class="flex flex-col sm:flex-row gap-2 pt-1 border-t border-border/60">
              <input v-model="bindTarget.email" class="input-field !py-2 flex-1" placeholder="接收提醒的邮箱地址" />
              <button class="btn-brand !py-2 shrink-0" :disabled="busy" @click="bind('email')">添加邮箱</button>
            </div>
            <ul v-if="ch.bindings?.length" class="space-y-1.5">
              <li
                v-for="b in ch.bindings"
                :key="b.id"
                class="flex items-center justify-between gap-2 text-xs rounded-lg bg-muted/60 px-2.5 py-1.5"
              >
                <span class="text-muted-foreground">{{ b.target_masked }}</span>
                <button class="text-destructive hover:underline" :disabled="busy" @click="removeBinding(ch.channel, b.id)">删除</button>
              </li>
            </ul>
          </template>

          <!-- 短信：自备网关 -->
          <template v-else-if="ch.channel === 'sms'">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">短信网关 Webhook 地址</span>
                <input v-model="smsForm.webhook_url" class="input-field !py-1.5" placeholder="https://…" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">网关 Token（选填）</span>
                <input v-model="smsForm.webhook_token" class="input-field !py-1.5" placeholder="留空不校验" />
              </label>
            </div>
            <p class="text-[11px] text-muted-foreground">
              应用会 POST { phone, title, body } 到该地址，由网关负责实际下发，适配任意自建短信服务。
            </p>
            <button class="btn-ghost !py-1.5" :disabled="busy" @click="saveProvider('sms')">保存网关配置</button>
            <div class="flex flex-col sm:flex-row gap-2 pt-1 border-t border-border/60">
              <input v-model="bindTarget.sms" class="input-field !py-2 flex-1" placeholder="接收提醒的手机号" />
              <button class="btn-brand !py-2 shrink-0" :disabled="busy" @click="bind('sms')">绑定手机号</button>
            </div>
          </template>

          <!-- QQ 机器人：应用配置 + 绑定码 -->
          <template v-else-if="ch.channel === 'qq'">
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">App ID</span>
                <input v-model="qqForm.app_id" class="input-field !py-1.5" placeholder="QQ 开放平台机器人 AppID" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">App Secret</span>
                <input v-model="qqForm.app_secret" type="password" class="input-field !py-1.5" placeholder="已配置则留空不改" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block mb-1">机器人主页（选填）</span>
                <input v-model="qqForm.bot_link" class="input-field !py-1.5" placeholder="https://…" />
              </label>
            </div>
            <button class="btn-ghost !py-1.5" :disabled="busy" @click="saveProvider('qq')">保存 QQ 机器人配置</button>
            <div class="pt-1 border-t border-border/60 space-y-2">
              <button class="btn-brand !py-2" :disabled="busy" @click="makeQQCode">生成绑定码</button>
              <p v-if="qqCode" class="text-sm">
                绑定码 <strong class="text-brand-600 dark:text-brand-300 text-lg tabular-nums tracking-widest mx-1">{{ qqCode }}</strong>
                （10 分钟内有效）
              </p>
              <p class="text-[11px] text-muted-foreground">
                给机器人发送私聊消息「/绑定 绑定码」即可完成绑定，无需手动找 OpenID。
              </p>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { toast } from '../utils/toast'
import {
  bindNotifyChannel, createQQBindCode, deleteNotifyBinding, getNotifyChannels, getNotifyProvider,
  saveNotifyProvider, testNotifyChannel, toggleNotifyChannel,
  type NotifyChannel, type NotifyChannelStatus,
} from '../api/rental'

const channels = ref<NotifyChannelStatus[]>([])
const loading = ref(false)
const busy = ref(false)
const expanded = ref('')
const qqCode = ref('')

// 绑定输入（按渠道一份）
const bindTarget = reactive<Record<string, string>>({ dingtalk: '', email: '', sms: '', qq: '' })
// 服务商配置表单：敏感字段留空表示沿用旧值
const emailForm = reactive({ host: '', port: '', from_address: '', from_name: '', username: '', password: '' })
const smsForm = reactive({ webhook_url: '', webhook_token: '' })
const qqForm = reactive({ app_id: '', app_secret: '', bot_link: '' })

onMounted(load)

async function load() {
  loading.value = true
  try {
    const res = await getNotifyChannels()
    if (res.data?.code === 0) channels.value = res.data.data ?? []
    // 回显非敏感配置（密码/密钥不回传）。
    const [e, s, q] = await Promise.all([
      getNotifyProvider('email'), getNotifyProvider('sms'), getNotifyProvider('qq'),
    ])
    if (e.data?.code === 0) {
      const d = e.data.data
      emailForm.host = d.host || ''
      emailForm.port = d.port || ''
      emailForm.from_address = d.from_address || ''
      emailForm.from_name = d.from_name || ''
      emailForm.username = d.username || ''
    }
    if (s.data?.code === 0) smsForm.webhook_url = s.data.data.webhook_url || ''
    if (q.data?.code === 0) {
      qqForm.app_id = q.data.data.app_id || ''
      qqForm.bot_link = q.data.data.bot_link || ''
    }
  } catch {
    toast('加载通知渠道失败', 'error')
  } finally {
    loading.value = false
  }
}

function statusText(ch: NotifyChannelStatus): string {
  if (ch.bound && ch.status === 'active' && ch.configured) return '已启用'
  if (ch.bound && ch.status === 'active') return '待配置'
  if (ch.bound) return '已停用'
  return '未绑定'
}

function statusClass(ch: NotifyChannelStatus): string {
  if (ch.bound && ch.status === 'active' && ch.configured) return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  if (ch.bound && ch.status === 'active') return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return 'bg-muted text-muted-foreground'
}

function expandText(ch: NotifyChannelStatus): string {
  if (expanded.value === ch.channel) return '收起'
  if (ch.channel === 'email') return 'SMTP / 邮箱'
  if (ch.channel === 'sms') return '网关 / 手机'
  if (ch.channel === 'qq') return '配置 / 绑定'
  return ch.bound ? 'Webhook' : '绑定'
}

async function bind(channel: NotifyChannel) {
  const target = (bindTarget[channel] || '').trim()
  if (!target) {
    toast('请先填写绑定目标', 'error')
    return
  }
  busy.value = true
  try {
    const res = await bindNotifyChannel(channel, target)
    if (res.data?.code === 0) {
      toast('已保存绑定')
      bindTarget[channel] = ''
      await load()
    } else {
      toast(res.data?.message || '绑定失败', 'error')
    }
  } catch (err: any) {
    toast(err.response?.data?.message || '绑定失败', 'error')
  } finally {
    busy.value = false
  }
}

async function removeBinding(channel: NotifyChannel, id: number) {
  busy.value = true
  try {
    const res = await deleteNotifyBinding(channel, id)
    if (res.data?.code === 0) {
      toast('已删除')
      await load()
    } else {
      toast(res.data?.message || '删除失败', 'error')
    }
  } finally {
    busy.value = false
  }
}

async function toggle(ch: NotifyChannelStatus) {
  busy.value = true
  try {
    const res = await toggleNotifyChannel(ch.channel, ch.status !== 'active')
    if (res.data?.code === 0) {
      await load()
    } else {
      toast(res.data?.message || '操作失败', 'error')
    }
  } finally {
    busy.value = false
  }
}

async function test(ch: NotifyChannelStatus) {
  busy.value = true
  try {
    const res = await testNotifyChannel(ch.channel)
    if (res.data?.code === 0) {
      toast(`测试消息已发送到${ch.label}`)
    } else {
      toast(res.data?.message || '发送失败', 'error')
    }
  } catch (err: any) {
    toast(err.response?.data?.message || '发送失败', 'error')
  } finally {
    busy.value = false
  }
}

async function saveProvider(provider: 'email' | 'sms' | 'qq') {
  busy.value = true
  try {
    const values = provider === 'email' ? { ...emailForm } : provider === 'sms' ? { ...smsForm } : { ...qqForm }
    const res = await saveNotifyProvider(provider, values)
    if (res.data?.code === 0) {
      toast('配置已保存')
      await load()
    } else {
      toast(res.data?.message || '保存失败', 'error')
    }
  } catch (err: any) {
    toast(err.response?.data?.message || '保存失败', 'error')
  } finally {
    busy.value = false
  }
}

async function makeQQCode() {
  busy.value = true
  qqCode.value = ''
  try {
    const res = await createQQBindCode()
    if (res.data?.code === 0) {
      qqCode.value = res.data.data.code
    } else {
      toast(res.data?.message || '生成绑定码失败', 'error')
    }
  } catch (err: any) {
    toast(err.response?.data?.message || '生成绑定码失败', 'error')
  } finally {
    busy.value = false
  }
}
</script>
