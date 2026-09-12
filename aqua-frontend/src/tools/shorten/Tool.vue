<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
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
  <div class="tool-io">
    <div class="tool-btns" style="width:100%;">
      <input v-model="url" placeholder="https://example.com/very/long/url?with=params" style="flex:1;min-width:220px;background:var(--card2);color:var(--text);border:1px solid var(--border);border-radius:10px;padding:10px 14px;font-family:var(--mono);">
      <button class="btn tool-run" @click="run">缩短</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">生成中…</div>
      <div v-else-if="result" class="tool-ai-box">
        <b>短链已生成</b>
        <div class="pg-hrow" style="margin-top:8px;">
          <span style="user-select:all;color:var(--accent);font-family:var(--mono);">{{ result.short }}</span>
          <CopyBtn :text="result.short" />
        </div>
        <div style="font-size:11.5px;color:var(--muted);margin-top:6px;">目标：{{ result.url }}<template v-if="result.retention"> · {{ result.retention }}</template></div>
      </div>
    </div>
  </div>
</template>
