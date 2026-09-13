<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>要估算的文本</label>
        <textarea v-model="text" class="textarea" rows="10" placeholder="粘贴要估算的文本…"></textarea>
      </div>
      <div class="row">
        <button class="btn primary" :disabled="loading" @click="run"><svg class="bic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS['token-count']"></svg>估算</button>
        <span v-if="note" class="tool-status">{{ note }}</span>
      </div>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <div v-else-if="loading" class="out-pane">
        <div class="skeleton" style="width: 55%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 80%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 66%; min-height: 13px;"></div>
      </div>
      <div v-else-if="result" class="tool-kv">
        <div><span>估算 Tokens</span><b style="color: var(--acc);">{{ result.tokens }}</b></div>
        <div><span>总字符数</span><b>{{ result.chars }}</b></div>
        <div><span>其中中文字符</span><b>{{ result.cjk_chars }}</b></div>
      </div>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS['token-count']"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
.bic { width: 14px; height: 14px; }
</style>
