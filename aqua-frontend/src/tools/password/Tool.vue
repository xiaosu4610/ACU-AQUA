<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import CopyBtn from '@/components/CopyBtn.vue'

const len = ref(16)
const cnt = ref(5)
const upper = ref(true)
const lower = ref(true)
const digits = ref(true)
const symbols = ref(false)
const loading = ref(false)
const msg = ref('')
const list = ref<string[]>([])

async function run() {
  loading.value = true
  msg.value = ''
  list.value = []
  try {
    const j = await apiJson<{ passwords: string[] }>('/tools/password', {
      body: {
        length: parseInt(String(len.value), 10) || 16,
        count: parseInt(String(cnt.value), 10) || 5,
        upper: upper.value,
        lower: lower.value,
        digits: digits.value,
        symbols: symbols.value,
      },
    })
    list.value = j.passwords || []
  } catch (e) {
    msg.value = '生成失败：' + errText(e)
  }
  loading.value = false
}
</script>

<template>
  <p class="tool-intro">批量生成高强度随机密码——由网关服务端熵源生成，保证每类选中字符至少出现一次。</p>
  <div class="tool-bar">
    <span>长度</span>
    <input v-model.number="len" type="number" min="8" max="64" value="16" style="width:70px;background:var(--card2);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:7px 10px;font-family:var(--mono);">
    <span>数量</span>
    <input v-model.number="cnt" type="number" min="1" max="20" value="5" style="width:70px;background:var(--card2);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:7px 10px;font-family:var(--mono);">
  </div>
  <div class="tool-bar">
    <span>字符类型</span>
    <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="upper" type="checkbox">大写</label>
    <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="lower" type="checkbox">小写</label>
    <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="digits" type="checkbox">数字</label>
    <label style="display:inline-flex;align-items:center;gap:4px;"><input v-model="symbols" type="checkbox">符号</label>
    <button class="btn tool-run" @click="run">生成</button>
  </div>
  <div class="tool-result">
    <div v-if="msg" class="tool-empty">{{ msg }}</div>
    <div v-else-if="loading" class="tool-loading">生成中…</div>
    <div v-for="p in list" :key="p" class="pg-hrow" style="font-family:var(--mono);">
      <span style="user-select:all;">{{ p }}</span>
      <CopyBtn :text="p" />
    </div>
  </div>
</template>
