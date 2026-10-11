<template>
  <!-- 手机端页头收紧：标题 text-lg（原 text-xl）、说明文字隐藏。
       窄屏本来就只有 375px，标题+说明+整行按钮曾经吃掉 106px，列表被挤到屏幕外；
       桌面（sm 起）保持原样：大标题 + 说明 + 右侧操作组。
       标题**不能 truncate**（账单页 4 个按钮会把「抄表账单」压成「抄表…」，
       违反「内容不得截断」底线），所以标题与操作分两行：标题独占一行，
       操作组换行到下面（按钮多时也不会跟标题挤在一起、更不会盖住标题）。 -->
  <div class="space-y-2 sm:flex sm:flex-wrap sm:items-start sm:justify-between sm:gap-3 sm:space-y-0">
    <div class="min-w-0 sm:flex-none">
      <h1 class="text-lg sm:text-xl font-display font-bold text-foreground">{{ title }}</h1>
      <p v-if="description" class="hidden sm:block text-sm text-muted-foreground mt-1">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-1.5 sm:gap-2 sm:shrink-0">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string
    description?: string
  }>(),
  { description: '' },
)
</script>
