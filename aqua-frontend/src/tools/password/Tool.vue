<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
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
  <div class="grid2">
    <div class="card out-pane">
      <div class="form-grid">
        <div class="field">
          <label>长度（8-64）</label>
          <input v-model.number="len" class="input mono" type="number" min="8" max="64">
        </div>
        <div class="field">
          <label>数量（1-20）</label>
          <input v-model.number="cnt" class="input mono" type="number" min="1" max="20">
        </div>
      </div>
      <div class="row wrap">
        <span class="dim" style="font-size: 12.5px; font-weight: 600;">字符类型</span>
        <label class="opt"><input v-model="upper" type="checkbox">大写</label>
        <label class="opt"><input v-model="lower" type="checkbox">小写</label>
        <label class="opt"><input v-model="digits" type="checkbox">数字</label>
        <label class="opt"><input v-model="symbols" type="checkbox">符号</label>
      </div>
      <button class="btn primary" :disabled="loading" @click="run"><svg class="bic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.password"></svg>生成</button>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <div v-else-if="loading" class="out-pane">
        <div class="skeleton" style="width: 78%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 70%; min-height: 13px;"></div>
        <div class="skeleton" style="width: 84%; min-height: 13px;"></div>
      </div>
      <template v-else-if="list.length">
        <div class="tool-kv mono">
          <div v-for="p in list" :key="p">
            <span style="user-select: all; color: var(--txt0);">{{ p }}</span>
            <b><CopyBtn :text="p" size="xs" /></b>
          </div>
        </div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.password"></svg><span>点击「生成」批量产出强密码</span></div>
    </div>
  </div>
</template>

<style scoped>
.opt { display: inline-flex; align-items: center; gap: 5px; font-size: 13px; color: var(--txt2); cursor: pointer; user-select: none; }
.ticon { width: 26px; height: 26px; opacity: .55; }
.bic { width: 14px; height: 14px; }
</style>
