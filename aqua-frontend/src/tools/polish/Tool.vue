<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>原文</label>
        <textarea v-model="text" class="textarea" rows="9" placeholder="粘贴要润色的文字：文案 / 朋友圈 / 产品介绍 / 邮件…"></textarea>
      </div>
      <div class="row wrap">
        <div class="chips">
          <button v-for="m in MODES" :key="m" class="chip" :class="{ on: mode === m }" type="button" @click="mode = m">{{ m }}</button>
        </div>
        <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="spark" :size="14" />开始润色</button>
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
        <div class="row between"><b>{{ mode }}版</b><CopyBtn :text="out" /></div>
        <div class="tool-prose">{{ out }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.polish"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
