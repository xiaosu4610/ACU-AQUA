<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

const MODES = [
  { value: 'points', label: '要点列表' },
  { value: 'para', label: '一段话' },
  { value: 'outline', label: '大纲结构' },
]
// 三种摘要模式对应三种提示词
const ASKS: Record<string, string> = {
  points: '请将以下文本提炼为 3-6 条核心要点，每条一行，以「- 」开头，按重要性排序，不要废话。',
  para: '请将以下文本总结为一段话（100 字以内），保留最核心的信息。',
  outline: '请将以下文本整理为层级大纲（用缩进的短句表示层级结构，最多三层），覆盖全部关键信息。',
}

const text = ref('')
const mode = ref('points')
const loading = ref(false)
const msg = ref('')
const out = ref('')

async function run() {
  msg.value = ''
  out.value = ''
  if (!text.value.trim()) { msg.value = '请先输入要总结的文本。'; return }
  loading.value = true
  try {
    out.value = await apiChat([
      { role: 'system', content: '你是一位专业的内容总结助手。' },
      { role: 'user', content: (ASKS[mode.value] || ASKS.points) + '\n\n【文本】\n' + text.value },
    ], { temperature: 0.3, max_tokens: 2048 })
  } catch (e) {
    msg.value = '生成失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">粘贴长文，一键提炼为要点列表、一段话总结或文章大纲。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="8" placeholder="粘贴文章、报告、聊天记录等长文本…"></textarea>
    <div class="tool-bar">
      <span>模式</span>
      <label v-for="m in MODES" :key="m.value" style="display:inline-flex;align-items:center;gap:4px;">
        <input v-model="mode" type="radio" name="sum-mode" :value="m.value">{{ m.label }}
      </label>
      <button class="btn tool-run" @click="run">生成摘要</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">生成中…</div>
      <div v-else-if="out" class="tool-ai-box"><b>摘要结果</b><div class="tool-ai-text">{{ out }}</div></div>
    </div>
  </div>
</template>
