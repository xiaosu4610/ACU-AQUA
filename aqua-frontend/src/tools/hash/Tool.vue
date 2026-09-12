<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'

const text = ref('')
const algo = ref('sha256')
const loading = ref(false)
const msg = ref('')
const result = ref<{ algo: string; digest: string } | null>(null)

const ALGOS = ['md5', 'sha1', 'sha256', 'sha512']

async function run() {
  msg.value = ''
  result.value = null
  if (!text.value) { msg.value = '请先输入要计算哈希的文本。'; return }
  loading.value = true
  try {
    const j = await apiJson<{ algo: string; digest: string }>('/tools/hash', {
      body: { text: text.value, algo: algo.value },
    })
    result.value = { algo: String(j.algo || algo.value).toUpperCase(), digest: j.digest }
  } catch (e) {
    msg.value = '计算失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">计算文本的 MD5 / SHA1 / SHA256 / SHA512 哈希值——常用于校验下载文件指纹、比对内容是否被篡改。</p>
  <div class="tool-io">
    <textarea v-model="text" rows="6" placeholder="输入任意文本…"></textarea>
    <div class="tool-bar">
      <span>算法</span>
      <label v-for="a in ALGOS" :key="a" style="display:inline-flex;align-items:center;gap:4px;">
        <input v-model="algo" type="radio" name="hs-algo" :value="a">{{ a.toUpperCase() }}
      </label>
      <button class="btn tool-run" @click="run">计算</button>
    </div>
    <div class="tool-result">
      <div v-if="msg" class="tool-empty">{{ msg }}</div>
      <div v-else-if="loading" class="tool-loading">计算中…</div>
      <div v-else-if="result" class="tool-ai-box">
        <b>{{ result.algo }} 摘要</b>
        <div class="tool-ai-text" style="word-break:break-all;font-family:var(--mono);font-size:12.5px;">{{ result.digest }}</div>
      </div>
    </div>
  </div>
</template>
