<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>本周流水账</label>
        <textarea v-model="text" class="textarea" rows="10" placeholder="例：修了登录页 bug；和产品对了新需求；看了 Redis 持久化的文章；周五团建…"></textarea>
      </div>
      <div class="row wrap">
        <div class="chips">
          <button v-for="t in TONES" :key="t" class="chip" :class="{ on: tone === t }" type="button" @click="tone = t">{{ t }}</button>
        </div>
        <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="spark" :size="14" />生成周报</button>
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
        <div class="row between"><b>周报草稿</b><CopyBtn :text="out" /></div>
        <div class="tool-prose">{{ out }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.weekly"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
