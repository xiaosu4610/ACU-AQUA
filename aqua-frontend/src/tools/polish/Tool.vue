<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

const MODES = ['润色', '极简', '扩写']
const ASKS: Record<string, string> = {
  润色: '保持原意只改表达：删冗余、换具象词、调节奏。输出润色版，再用一句话说明主要改了什么。',
  极简: '把原意压缩到一半篇幅，只保留最核心的信息与最有力的表达。输出极简版。',
  扩写: '在不编造事实的前提下扩写：补充细节、画面感与情绪层次，篇幅约为原文 1.5-2 倍。输出扩写版。',
}

const text = ref('')
const mode = ref('润色')
const loading = ref(false)
const msg = ref('')
const out = ref('')

async function run() {
  msg.value = ''
  out.value = ''
  if (!text.value.trim()) { msg.value = '请先输入要润色的文字。'; return }
  loading.value = true
  const ask = ASKS[mode.value] || ASKS['润色']
  try {
    out.value = await apiChat([
      { role: 'system', content: '你是文案点睛手。禁止使用「赋能」「抓手」「闭环」这类词。' },
      { role: 'user', content: ask + '\n\n【原文】\n' + text.value },
    ], { temperature: 0.5, max_tokens: 2048 })
  } catch (e) {
    msg.value = '润色失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">不改本意只改表达：润色（更顺更有画面感）、极简（压缩一半）、扩写（更充实具体）。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="7" placeholder="粘贴要润色的文字：文案 / 朋友圈 / 产品介绍 / 邮件…"></textarea>
    <div class="tool-bar">
      <span>模式</span>
      <label v-for="m in MODES" :key="m" style="display:inline-flex;align-items:center;gap:4px;">
        <input v-model="mode" type="radio" name="pl-mode" :value="m">{{ m }}
      </label>
      <button class="btn tool-run" @click="run">开始润色</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">打磨中…</div>
      <div v-else-if="out" class="tool-ai-box"><b>{{ mode }}版</b><div class="tool-ai-text">{{ out }}</div></div>
    </div>
  </div>
</template>
