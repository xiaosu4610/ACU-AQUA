<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

const TONES = ['克制', '详细', '汇报体']

const text = ref('')
const tone = ref('克制')
const loading = ref(false)
const msg = ref('')
const out = ref('')

async function run() {
  msg.value = ''
  out.value = ''
  if (!text.value.trim()) { msg.value = '先写点本周做过的事，流水账也行。'; return }
  loading.value = true
  try {
    out.value = await apiChat([
      { role: 'system', content: '你是周报炼金师。输出结构：1. 本周核心产出（3-5 条，动词开头，突出结果与数据，不夸大）；2. 进行中事项（进度与下一步）；3. 风险与需要的支持；4. 下周计划。语言' + tone.value + '，不写空话套话，缺失信息用【待补充】标注而不是编造。' },
      { role: 'user', content: '我的本周流水账：\n' + text.value },
    ], { temperature: 0.4, max_tokens: 2048 })
  } catch (e) {
    msg.value = '生成失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">把本周做过的零散事项（流水账即可）交给周报炼金师，产出结构化、有亮点的正式周报。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="8" placeholder="例：修了登录页 bug；和产品对了新需求；看了 Redis 持久化的文章；周五团建…"></textarea>
    <div class="tool-bar">
      <span>语气</span>
      <label v-for="t in TONES" :key="t" style="display:inline-flex;align-items:center;gap:4px;">
        <input v-model="tone" type="radio" name="wk-tone" :value="t">{{ t }}
      </label>
      <button class="btn tool-run" @click="run">生成周报</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">炼制中…</div>
      <div v-else-if="out" class="tool-ai-box"><b>周报草稿</b><div class="tool-ai-text">{{ out }}</div></div>
    </div>
  </div>
</template>
