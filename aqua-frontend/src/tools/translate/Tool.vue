<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

const TOOL_LANGS = ['自动检测', '中文', '英语', '日语', '韩语', '法语', '德语', '西班牙语', '俄语', '阿拉伯语', '葡萄牙语']

const text = ref('')
const lang = ref('自动检测')
const loading = ref(false)
const msg = ref('')
const out = ref('')

async function run() {
  msg.value = ''
  out.value = ''
  if (!text.value.trim()) { msg.value = '请先输入要翻译的文本。'; return }
  loading.value = true
  const sys = '你是一位专业翻译。将用户提供的文本翻译成' + (lang.value === '自动检测' ? '与原文不同的最合适的语言' : lang.value) +
    '。要求：保留原文格式与语气；专有名词保留原文并可在括号内标注；只输出译文，不要解释。'
  try {
    out.value = await apiChat([
      { role: 'system', content: sys },
      { role: 'user', content: text.value },
    ], { temperature: 0.2, max_tokens: 4096 })
  } catch (e) {
    msg.value = '翻译失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">多语种互译，源语言可自动检测。底层由 AQUA 网关对话模型驱动，译文自然流畅。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="6" placeholder="粘贴要翻译的文本…"></textarea>
    <div class="tool-bar">
      <span>译为</span>
      <select v-model="lang">
        <option v-for="l in TOOL_LANGS" :key="l" :value="l">{{ l }}</option>
      </select>
      <button class="btn tool-run" @click="run">开始翻译</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">翻译中…</div>
      <div v-else-if="out" class="tool-ai-box"><b>译文</b><div class="tool-ai-text">{{ out }}</div></div>
    </div>
  </div>
</template>
