<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import { apiChat } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'

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
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>IP 地址（留空查询你的出口 IP）</label>
        <textarea v-model="input" class="textarea" rows="2" placeholder="例如 8.8.8.8 或 114.114.114.114" style="min-height: 0;"></textarea>
      </div>
      <div class="row">
        <button class="btn primary" :disabled="loading" @click="run"><AqIcon name="grid" :size="14" />查询</button>
        <button v-show="rows.length" class="btn" @click="aiExplain"><AqIcon name="spark" :size="14" />AI 解读</button>
      </div>
    </div>
    <div class="card out-pane">
      <div v-if="explain" class="out-pane">
        <div class="row between"><b>AI 解读</b></div>
        <div class="tool-prose">{{ explain.loading ? '正在生成解读…' : explain.text }}</div>
      </div>
      <template v-else>
        <div v-if="loading" class="out-pane">
          <div class="skeleton" style="width: 46%; min-height: 13px;"></div>
          <div class="skeleton" style="width: 88%; min-height: 13px;"></div>
          <div class="skeleton" style="width: 70%; min-height: 13px;"></div>
        </div>
        <div v-else-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
        <div v-else-if="rows.length" class="tool-kv">
          <div v-for="(p, i) in rows" :key="i"><span>{{ p[0] }}</span><b>{{ p[1] }}</b></div>
        </div>
        <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.ip"></svg><span>结果将显示在这里</span></div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
