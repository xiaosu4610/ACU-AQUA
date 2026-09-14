<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

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
  <p class="tool-intro">多语种互译，源语言可自动检测。底层由 AQUA api 网关对话模型驱动，译文自然流畅。</p>
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>原文</label>
        <textarea v-model="text" class="textarea" rows="8" placeholder="粘贴要翻译的文本…"></textarea>
      </div>
      <div class="row">
        <span class="dim" style="font-size: 12.5px; font-weight: 600;">译为</span>
        <select v-model="lang" class="select" style="width: auto; flex: none;">
          <option v-for="l in TOOL_LANGS" :key="l" :value="l">{{ l }}</option>
        </select>
        <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="spark" :size="14" />开始翻译</button>
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
        <div class="row between"><b>译文</b><CopyBtn :text="out" /></div>
        <div class="tool-prose">{{ out }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.translate"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
