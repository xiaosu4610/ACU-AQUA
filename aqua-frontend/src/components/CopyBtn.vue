<script setup lang="ts">
import { ref } from 'vue'
import { copyText } from '@/composables/useApi'

const props = defineProps<{ text: string; label?: string }>()
const copied = ref(false)
async function doCopy() {
  if (await copyText(props.text)) {
    copied.value = true
    setTimeout(() => (copied.value = false), 1400)
  }
}
</script>

<template>
  <button class="btn" :class="{ copied }" @click="doCopy">
    <template v-if="copied"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" style="width:13px;height:13px"><path d="M20 6 9 17l-5-5"/></svg>已复制</template>
    <template v-else>{{ label || '复制' }}</template>
  </button>
</template>
