<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

const TOOL_MOD_MODEL = 'security-semantic-filtering'  // 上游实测唯一支持文本审核的模型

const text = ref('')
const loading = ref(false)
const msg = ref('')
const rows = ref<[string, string][]>([])
const lastData = ref<any>(null)
const explain = ref<{ loading: boolean; text: string } | null>(null)

async function run() {
  msg.value = ''
  rows.value = []
  lastData.value = null
  explain.value = null
  if (!text.value.trim()) { msg.value = '请先输入待检测的文本。'; return }
  loading.value = true
  try {
    const j = await apiJson<any>('/moderations', {
      method: 'POST',
      body: { model: TOOL_MOD_MODEL, input: text.value.trim() },
    })
    lastData.value = j
    const flat: [string, string][] = []
    // 递归展平 JSON 为「字段 · 值」行，方便表格展示
    ;(function walk(o: any, prefix: string) {
      Object.keys(o || {}).forEach(k => {
        const v = o[k]
        const key = prefix ? prefix + ' · ' + k : k
        if (v && typeof v === 'object') walk(v, key)
        else flat.push([key, String(v)])
      })
    })(j, '')
    if (!flat.length) { msg.value = '接口未返回有效数据，请稍后重试。'; return }
    rows.value = flat
  } catch (e) {
    msg.value = '检测失败：' + errText(e)
  }
  loading.value = false
}

async function aiExplain() {
  if (!lastData.value) return
  const prompt = '以下是一段用户文本和内容审核接口返回的 JSON 结果（类别含 politic/porn/insult/violence）。请用中文分析：1) 该文本是否存在风险，风险点是什么；2) 如果有风险，如何改写可以降低风险。共 3-4 句话，通俗易懂。\n\n【用户文本】\n' + text.value.trim() + '\n\n【审核结果 JSON】\n' + JSON.stringify(lastData.value)
  explain.value = { loading: true, text: '' }
  try {
    explain.value = { loading: false, text: await apiChat([{ role: 'user', content: prompt }], { max_tokens: 500 }) }
  } catch (e) {
    explain.value = { loading: false, text: 'AI 解读暂不可用（' + errText(e) + '），基础查询结果不受影响。' }
  }
}
</script>

<template>
  <p class="tool-intro">输入任意文本，检测是否包含涉政、色情、辱骂、暴力等风险内容；AI 会进一步解释风险点并给出改写建议。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="5" placeholder="粘贴待检测的文本内容…"></textarea>
    <div class="tool-btns">
      <button class="btn tool-run" @click="run">开始检测</button>
      <button v-show="rows.length" class="btn tool-ai" @click="aiExplain">AI 解读</button>
    </div>
    <div class="tool-result">
      <div v-if="explain" class="tool-ai-box">
        <b>AI 解读</b>
        <div class="tool-ai-text">{{ explain.loading ? '正在生成解读…' : explain.text }}</div>
      </div>
      <template v-else>
        <div v-if="loading" class="tool-loading">检测中…</div>
        <div v-else-if="msg" class="tool-empty">{{ msg }}</div>
        <div v-else-if="rows.length" class="tool-kv">
          <div v-for="(p, i) in rows" :key="i" class="pg-hrow"><span>{{ p[0] }}</span><b>{{ p[1] }}</b></div>
        </div>
      </template>
    </div>
  </div>
</template>
