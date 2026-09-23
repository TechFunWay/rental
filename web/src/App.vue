<template>
  <router-view />
  <Toast :message="toastText(toastState.message)" :type="toastState.type" :key="toastState.message" />
</template>

<script setup lang="ts">
import Toast from './components/Toast.vue'
import { toastState, toastText } from './utils/toast'
</script>
