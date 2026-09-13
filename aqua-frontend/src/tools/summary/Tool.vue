<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>原文</label>
        <textarea v-model="text" class="textarea" rows="10" placeholder="粘贴文章、报告、聊天记录等长文本…"></textarea>
      </div>
      <div class="row wrap">
        <div class="chips">
          <button v-for="m in MODES" :key="m.value" class="chip" :class="{ on: mode === m.value }" type="button" @click="mode = m.value">{{ m.label }}</button>
        </div>
        <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="spark" :size="14" />生成摘要</button>
      </div>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <div v-else-if="loading" class="out-pane">
        <div class="skeleton" style="width: 52%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 90%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 74%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 40%; min-height: 13px;"></div>
      </div>
      <template v-else-if="out">
        <div class="row between"><b>摘要结果</b><CopyBtn :text="out" /></div>
        <div class="tool-prose">{{ out }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.summary"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
