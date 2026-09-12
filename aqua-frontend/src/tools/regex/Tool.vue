<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

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
  <div class="tool-io">
    <div class="tool-bar">
      <span>方向</span>
      <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="dir" type="radio" name="rg-dir" value="to-regex">描述 → 正则</label>
      <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="dir" type="radio" name="rg-dir" value="to-human">正则 → 解释</label>
    </div>
    <textarea v-model="text" rows="6" placeholder="输入需求描述或正则表达式…"></textarea>
    <div class="tool-bar">
      <button class="btn tool-run" @click="run">转换</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">转换中…</div>
      <div v-else-if="out" class="tool-ai-box"><b>转换结果</b><div class="tool-ai-text">{{ out }}</div></div>
    </div>
  </div>
</template>
