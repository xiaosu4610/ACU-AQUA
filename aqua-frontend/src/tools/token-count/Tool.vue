<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'

const text = ref('')
const loading = ref(false)
const note = ref('')
const msg = ref('')
const result = ref<{ tokens: number; chars: number; cjk_chars: number } | null>(null)

async function run() {
  msg.value = ''
  result.value = null
  note.value = ''
  if (!text.value.trim()) { msg.value = '请先输入要估算的文本。'; return }
  loading.value = true
  try {
    const j = await apiJson<{ tokens: number; chars: number; cjk_chars: number; note?: string }>('/tools/token-count', {
      body: { text: text.value },
    })
    note.value = j.note || ''
    result.value = { tokens: j.tokens, chars: j.chars, cjk_chars: j.cjk_chars }
  } catch (e) {
    msg.value = '估算失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">估算一段文本会消耗多少 token（中文约 0.6 字/token，英文约 4 字符/token）——发送前心里有数，避免超长报错。为近似估算值，不同模型分词器有差异。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="8" placeholder="粘贴要估算的文本…"></textarea>
    <div class="tool-bar">
      <button class="btn tool-run" @click="run">估算</button>
      <span class="tool-status">{{ note }}</span>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">估算中…</div>
      <template v-else-if="result">
        <div class="pg-hrow"><span>估算 Tokens</span><b style="color:var(--accent);font-family:var(--mono);">{{ result.tokens }}</b></div>
        <div class="pg-hrow"><span>总字符数</span><b>{{ result.chars }}</b></div>
        <div class="pg-hrow"><span>其中中文字符</span><b>{{ result.cjk_chars }}</b></div>
      </template>
    </div>
  </div>
</template>
