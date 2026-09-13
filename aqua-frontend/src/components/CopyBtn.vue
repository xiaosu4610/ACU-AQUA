<script setup lang="ts">
/* 复制按钮：copyText 成功后短暂打勾 */
import { ref } from 'vue'
import { copyText } from '@/composables/useApi'
import AqIcon from './AqIcon.vue'

const props = withDefaults(defineProps<{ text: string; label?: string; size?: 'sm' | 'xs' }>(), { label: '复制' })
const ok = ref(false)
async function doCopy() {
  if (await copyText(props.text)) {
    ok.value = true
    setTimeout(() => (ok.value = false), 1400)
  }
}
</script>

<template>
  <button class="btn" :class="[size === 'xs' ? 'xs' : 'sm', { primary: ok }]" type="button" @click="doCopy">
    <AqIcon :name="ok ? 'check' : 'copy'" :size="13" />{{ ok ? '已复制' : label }}
  </button>
</template>
