<script setup lang="ts">
import { computed, ref } from 'vue'

const input = ref('')

const stats = computed(() => {
  const text = input.value
  if (!text) return null
  const chars = text.length
  const noSpace = text.replace(/\s/g, '').length
  const cjk = (text.match(/[\u4e00-\u9fa5]/g) || []).length
  const words = text.toLowerCase().match(/[a-z0-9]+(?:'[a-z]+)?/g) || []
  const lines = text.split(/\n/).length
  const sentences = (text.match(/[.!?。！？]+/g) || []).length || 1
  const readMin = Math.max(1, Math.round((cjk + words.length) / 400))
  const freq: Record<string, number> = {}
  words.forEach(w => { if (w.length > 2) freq[w] = (freq[w] || 0) + 1 })
  const top = Object.keys(freq).sort((a, b) => freq[b] - freq[a]).slice(0, 10).map(w => ({ w, n: freq[w] }))
  return { chars, noSpace, cjk, words: words.length, lines, sentences, readMin, top }
})
</script>

<template>
  <p class="tool-intro">纯算法实时统计：边输入边出结果，不经任何 AI，零额度消耗。同样能力可通过 <code>POST /v1/tools/text-stats</code> 调用。</p>
  <div class="tool-io">
    <textarea v-model="input" rows="8" placeholder="粘贴或输入任意文本，实时统计…"></textarea>
    <div class="tool-result">
      <div v-if="!stats" class="tool-empty">等待输入…</div>
      <template v-else>
        <div class="tool-kv">
          <div class="pg-hrow"><span>总字符（含空格换行）</span><b>{{ stats.chars }}</b></div>
          <div class="pg-hrow"><span>净字符（去空白）</span><b>{{ stats.noSpace }}</b></div>
          <div class="pg-hrow"><span>中文字数</span><b>{{ stats.cjk }}</b></div>
          <div class="pg-hrow"><span>英文单词数</span><b>{{ stats.words }}</b></div>
          <div class="pg-hrow"><span>行数 / 句数</span><b>{{ stats.lines }} / {{ stats.sentences }}</b></div>
          <div class="pg-hrow"><span>预计阅读时长</span><b>约 {{ stats.readMin }} 分钟</b></div>
        </div>
        <div v-if="stats.top.length" class="tool-kv">
          <div class="pg-hrow"><span>英文词频 Top10</span><b>次数</b></div>
          <div v-for="t in stats.top" :key="t.w" class="pg-hrow"><span>{{ t.w }}</span><b>{{ t.n }}</b></div>
        </div>
      </template>
    </div>
  </div>
</template>
