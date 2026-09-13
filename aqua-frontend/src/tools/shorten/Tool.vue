<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

const url = ref('')
const loading = ref(false)
const msg = ref('')
const result = ref<{ short: string; url: string; retention: string } | null>(null)

async function run() {
  msg.value = ''
  result.value = null
  const u = url.value.trim()
  if (!u) { msg.value = '请先输入要缩短的网址。'; return }
  loading.value = true
  try {
    const j = await apiJson<{ short: string; url: string; retention?: string }>('/tools/shorten', {
      body: { url: u },
    })
    result.value = { short: j.short, url: j.url, retention: j.retention || '' }
  } catch (e) {
    msg.value = '生成失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">把冗长网址压缩成 <code>https://acu.ltzy.top/s/xxxxxx</code> 短链，302 跳转直达。<b>90 天无访问自动清理</b>，不做长期留存。</p>
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>目标网址</label>
        <input v-model="url" class="input mono" placeholder="https://example.com/very/long/url?with=params" @keydown.enter="run">
      </div>
      <button class="btn primary" :disabled="loading" @click="run"><svg class="bic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.shorten"></svg>缩短</button>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <div v-else-if="loading" class="out-pane">
        <div class="skeleton" style="width: 55%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 82%; min-height: 13px;"></div>
      </div>
      <template v-else-if="result">
        <div class="row between"><b>短链已生成</b><CopyBtn :text="result.short" /></div>
        <div class="code mono" style="color: var(--acc); user-select: all;">{{ result.short }}</div>
        <div class="dim" style="font-size: 12px;">目标：{{ result.url }}<template v-if="result.retention"> · {{ result.retention }}</template></div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.shorten"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
.bic { width: 14px; height: 14px; }
</style>
