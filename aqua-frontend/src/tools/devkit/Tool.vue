<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'

const TABS = [
  { id: 'uuid', label: 'UUID' },
  { id: 'ts', label: '时间戳' },
  { id: 'b64', label: 'Base64' },
  { id: 'url', label: 'URL' },
  { id: 'json', label: 'JSON' },
  { id: 'subnet', label: '子网计算' },
]
const tab = ref('uuid')

/* ---- UUID ---- */
const uuid = ref('')
function genUuid() {
  uuid.value = crypto.randomUUID
    ? crypto.randomUUID()
    : 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
        const r = Math.random() * 16 | 0
        return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16)
      })
}

/* ---- 时间戳 ---- */
const tsIn = ref('')
const tsRows = ref<[string, string][]>([])
function tsConv() {
  const v = tsIn.value.trim()
  const ts = v ? parseInt(v, 10) : Math.floor(Date.now() / 1000)
  const d = new Date(ts * 1000)
  const local = isNaN(d.getTime()) ? '无效时间戳' : d.toLocaleString()
  tsRows.value = [
    ['时间戳（秒）', String(ts)],
    ['时间戳（毫秒）', String(ts * 1000)],
    ['本地时间', local],
    ['UTC', d.toUTCString()],
  ]
}
tsConv()

/* ---- Base64 ---- */
const b64In = ref('')
const b64Out = ref('')
function b64Enc() {
  try { b64Out.value = btoa(unescape(encodeURIComponent(b64In.value))) }
  catch { b64Out.value = '编码失败' }
}
function b64Dec() {
  try { b64Out.value = decodeURIComponent(escape(atob(b64In.value.trim()))) }
  catch { b64Out.value = '解码失败：不是有效的 Base64' }
}

/* ---- URL ---- */
const urlIn = ref('')
const urlOut = ref('')
function urlEnc() { urlOut.value = encodeURIComponent(urlIn.value) }
function urlDec() {
  try { urlOut.value = decodeURIComponent(urlIn.value.trim()) }
  catch { urlOut.value = '解码失败' }
}

/* ---- JSON ---- */
const jsonIn = ref('')
const jsonOut = ref('')
function jsonFmt(min: boolean) {
  try { jsonOut.value = JSON.stringify(JSON.parse(jsonIn.value), null, min ? 0 : 2) }
  catch (e: any) { jsonOut.value = 'JSON 解析失败：' + (e?.message || e) }
}

/* ---- 子网计算：调用网关纯算法端点 POST /v1/tools/subnet（后端 Rust 实现，零上游消耗） ---- */
const snIn = ref('')
const snRows = ref<[string, string][]>([])
const snMsg = ref('等待计算…')
async function snCalc() {
  const raw = snIn.value.trim()
  if (!raw) { snMsg.value = '请先输入网段（CIDR），如 192.168.1.0/24'; snRows.value = []; return }
  snMsg.value = '计算中…'
  snRows.value = []
  try {
    const j = await apiJson<any>('/tools/subnet', { body: { cidr: raw } })
    snMsg.value = ''
    snRows.value = [
      ['规范网段', j.input],
      ['子网掩码', j.netmask],
      ['反掩码（通配符）', j.wildcard],
      ['二进制掩码', j.binary_netmask],
      ['网络地址', j.network],
      ['广播地址', j.broadcast],
      ['可用主机范围', j.first_host + ' ~ ' + j.last_host],
      ['地址总数', j.total_addresses],
      ['可用主机数', j.usable_hosts],
      ['地址分类', j.ip_class],
      ['作用域', j.scope],
      ['十进制 / 十六进制', j.ip_int + ' / ' + j.ip_hex],
    ]
  } catch (e) {
    snMsg.value = errText(e)
  }
}

onMounted(() => { genUuid() })
</script>

<template>
  <p class="tool-intro">常用开发小工具合集，纯算法本地运行零延迟。对应 API：<code>GET /v1/tools/uuid</code>、<code>GET/POST /v1/tools/timestamp</code>、<code>POST /v1/tools/base64</code>、<code>POST /v1/tools/subnet</code>。</p>
  <div class="dev-tabs">
    <button v-for="t in TABS" :key="t.id" class="pill" :class="{ active: tab === t.id }" type="button" @click="tab = t.id">{{ t.label }}</button>
  </div>

  <!-- UUID -->
  <div v-if="tab === 'uuid'" class="tool-io">
    <div class="tool-btns" style="width:100%;">
      <input class="tool-flex1" readonly :value="uuid" style="font-family:var(--mono);">
      <button class="btn tool-run" @click="genUuid">生成</button>
    </div>
  </div>

  <!-- 时间戳 -->
  <div v-else-if="tab === 'ts'" class="tool-io">
    <div class="tool-btns" style="width:100%;">
      <input v-model="tsIn" class="tool-flex1" placeholder="输入秒级时间戳，留空显示当前">
      <button class="btn tool-run" @click="tsIn = tsIn.trim()">转换</button>
    </div>
    <div class="tool-kv">
      <div v-for="(r, i) in tsRows" :key="i" class="pg-hrow"><span>{{ r[0] }}</span><b>{{ r[1] }}</b></div>
    </div>
  </div>

  <!-- Base64 -->
  <div v-else-if="tab === 'b64'" class="tool-io">
    <textarea v-model="b64In" rows="4" placeholder="输入文本…"></textarea>
    <div class="tool-btns">
      <button class="btn tool-run" @click="b64Enc">编码</button>
      <button class="btn tool-run" @click="b64Dec">解码</button>
    </div>
    <textarea v-model="b64Out" rows="4" readonly placeholder="结果…"></textarea>
  </div>

  <!-- URL -->
  <div v-else-if="tab === 'url'" class="tool-io">
    <textarea v-model="urlIn" rows="4" placeholder="输入文本…"></textarea>
    <div class="tool-btns">
      <button class="btn tool-run" @click="urlEnc">编码</button>
      <button class="btn tool-run" @click="urlDec">解码</button>
    </div>
    <textarea v-model="urlOut" rows="4" readonly placeholder="结果…"></textarea>
  </div>

  <!-- JSON -->
  <div v-else-if="tab === 'json'" class="tool-io">
    <textarea v-model="jsonIn" rows="8" placeholder="粘贴 JSON…"></textarea>
    <div class="tool-btns">
      <button class="btn tool-run" @click="jsonFmt(false)">格式化</button>
      <button class="btn tool-run" @click="jsonFmt(true)">压缩</button>
    </div>
    <textarea v-model="jsonOut" rows="8" readonly placeholder="结果…"></textarea>
  </div>

  <!-- 子网计算 -->
  <div v-else-if="tab === 'subnet'" class="tool-io">
    <div class="tool-btns" style="width:100%;">
      <input v-model="snIn" class="tool-flex1" placeholder="输入 IPv4 网段，如 192.168.1.0/24 或 10.0.0.88/26" style="font-family:var(--mono);" @keydown.enter="snCalc">
      <button class="btn tool-run" @click="snCalc">计算</button>
    </div>
    <div class="tool-kv">
      <div v-if="snMsg" class="tool-empty">{{ snMsg }}</div>
      <div v-for="(r, i) in snRows" :key="i" class="pg-hrow"><span>{{ r[0] }}</span><b>{{ r[1] }}</b></div>
    </div>
  </div>
</template>
