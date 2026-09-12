<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'

/* 查询结果字段 → 中文标签（未知字段原样展示，新增字段零维护成本） */
const IP_FIELD_LABELS: Record<string, string> = {
  ip: 'IP 地址', continent: '大洲', country: '国家/地区', province: '省份', city: '城市',
  isp: '运营商', lat: '纬度', lon: '经度', district: '区县', owner: '所属机构',
  code: '行政区划代码', asn: 'AS 号', radius: '定位半径',
}

const input = ref('')
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
  loading.value = true
  try {
    const j = await apiJson<any>('/ip_location', {
      method: 'POST',
      body: { model: 'ip-location', ip: input.value.trim() },
    })
    lastData.value = j
    const flat: [string, string][] = []
    // 递归展平 JSON；顶层字段映射为中文标签，嵌套字段保留完整路径
    ;(function walk(o: any, prefix: string) {
      Object.keys(o || {}).forEach(k => {
        const v = o[k]
        const label = (!prefix && IP_FIELD_LABELS[k]) ? IP_FIELD_LABELS[k] : k
        const key = prefix ? prefix + ' · ' + label : label
        if (v && typeof v === 'object') walk(v, key)
        else flat.push([key, String(v)])
      })
    })(j, '')
    if (!flat.length) { msg.value = '未查询到信息，请检查 IP 格式。'; return }
    rows.value = flat
  } catch (e) {
    msg.value = '查询失败：' + errText(e)
  }
  loading.value = false
}

async function aiExplain() {
  if (!lastData.value) return
  const prompt = '以下是一个 IP 归属地查询接口返回的 JSON 数据。请用中文写 2-3 句通俗分析：该 IP 的地理位置特征、可能的网络环境（家庭宽带/机房/公共DNS等）、以及这类 IP 常见的使用场景。不要罗列原始数据。\n\n' + JSON.stringify(lastData.value)
  explain.value = { loading: true, text: '' }
  try {
    explain.value = { loading: false, text: await apiChat([{ role: 'user', content: prompt }], { max_tokens: 500 }) }
  } catch (e) {
    explain.value = { loading: false, text: 'AI 解读暂不可用（' + errText(e) + '），基础查询结果不受影响。' }
  }
}
</script>

<template>
  <p class="tool-intro">输入任意公网 IP 查询其地理位置与运营商；留空则查询<b>你当前访问本站所用的出口 IP</b>（由网关边缘节点识别）。查询结果可一键生成 AI 解读。</p>
  <div class="tool-io">
    <textarea v-model="input" rows="2" placeholder="例如 8.8.8.8 或 114.114.114.114，留空查询你的出口 IP"></textarea>
    <div class="tool-btns">
      <button class="btn tool-run" @click="run">查询</button>
      <button v-show="rows.length" class="btn tool-ai" @click="aiExplain">AI 解读</button>
    </div>
    <div class="tool-result">
      <div v-if="explain" class="tool-ai-box">
        <b>AI 解读</b>
        <div class="tool-ai-text">{{ explain.loading ? '正在生成解读…' : explain.text }}</div>
      </div>
      <template v-else>
        <div v-if="loading" class="tool-loading">查询中…</div>
        <div v-else-if="msg" class="tool-empty">{{ msg }}</div>
        <div v-else-if="rows.length" class="tool-kv">
          <div v-for="(p, i) in rows" :key="i" class="pg-hrow"><span>{{ p[0] }}</span><b>{{ p[1] }}</b></div>
        </div>
      </template>
    </div>
  </div>
</template>
