<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

const SYS: Record<string, string> = {
  'to-regex': '你是正则魔法师。用户给出需求描述时：生成正则表达式（放入代码块），给出 5 个匹配示例和 3 个不匹配示例，并逐段解释正则结构。标注 JS/Python/Java 方言差异。',
  'to-human': '你是正则魔法师。用户给出正则表达式时：逐段翻译成通俗中文，列出它能匹配什么、容易误匹配什么、常见输入下的行为。标注 JS/Python/Java 方言差异。',
}

const text = ref('')
const dir = ref('to-regex')
const loading = ref(false)
const msg = ref('')
const out = ref('')

async function run() {
  msg.value = ''
  out.value = ''
  if (!text.value.trim()) { msg.value = '请先输入内容。'; return }
  loading.value = true
  try {
    out.value = await apiChat([
      { role: 'system', content: SYS[dir.value] || SYS['to-regex'] },
      { role: 'user', content: text.value },
    ], { temperature: 0.2, max_tokens: 2048 })
  } catch (e) {
    msg.value = '转换失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">双向转换：给需求描述生成正则，或粘贴正则翻译成能看懂的人话。</p>
  <div class="grid2">
    <div class="card out-pane">
      <div class="chips">
        <button class="chip" :class="{ on: dir === 'to-regex' }" type="button" @click="dir = 'to-regex'">描述 → 正则</button>
        <button class="chip" :class="{ on: dir === 'to-human' }" type="button" @click="dir = 'to-human'">正则 → 解释</button>
      </div>
      <div class="field">
        <label>{{ dir === 'to-regex' ? '需求描述' : '正则表达式' }}</label>
        <textarea v-model="text" class="textarea mono" rows="8" placeholder="输入需求描述或正则表达式…"></textarea>
      </div>
      <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="spark" :size="14" />转换</button>
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
        <div class="row between"><b>转换结果</b><CopyBtn :text="out" /></div>
        <div class="tool-prose">{{ out }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.regex"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
