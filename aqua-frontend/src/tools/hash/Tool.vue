<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>输入文本</label>
        <textarea v-model="text" class="textarea" rows="8" placeholder="输入任意文本…"></textarea>
      </div>
      <div class="row wrap">
        <div class="chips">
          <button v-for="a in ALGOS" :key="a" class="chip mono" :class="{ on: algo === a }" type="button" @click="algo = a">{{ a.toUpperCase() }}</button>
        </div>
        <button class="btn primary" :disabled="loading" @click="run"><svg class="bic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.hash"></svg>计算</button>
      </div>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <div v-else-if="loading" class="out-pane">
        <div class="skeleton" style="width: 46%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 92%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 68%; min-height: 13px;"></div>
      </div>
      <template v-else-if="result">
        <div class="row between"><b>{{ result.algo }} 摘要</b><CopyBtn :text="result.digest" /></div>
        <div class="code" style="word-break: break-all;">{{ result.digest }}</div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.hash"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
.bic { width: 14px; height: 14px; }
</style>
