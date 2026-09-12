<script setup lang="ts">
import { ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import CopyBtn from '@/components/CopyBtn.vue'

interface WhItem { method: string; time: string; json: string }

const whId = ref('')
const busy = ref<'' | 'create' | 'list'>('')
const msg = ref('')            // tool-empty 文案（提示/已清空，含错误前缀的错误文案）
const noReqUrl = ref('')       // 空列表提示（还没有收到请求 + 收集地址）
const created = ref<{ url: string; usage: string } | null>(null)
const total = ref(0)
const items = ref<WhItem[]>([])

function resetOut() {
  msg.value = ''
  noReqUrl.value = ''
  created.value = null
  total.value = 0
  items.value = []
}

async function listReq(id: string) {
  busy.value = 'list'
  try {
    const j = await apiJson<{ items?: any[]; total?: number; url?: string }>('/tools/webhook', {
      body: { action: 'list', id },
    })
    resetOut()
    const arr = j.items || []
    if (!arr.length) {
      noReqUrl.value = j.url || ''
      return
    }
    total.value = j.total || 0
    items.value = arr.map((it: any) => ({
      method: String(it.method || ''),
      time: new Date((it.ts || 0) * 1000).toLocaleString(),
      json: JSON.stringify({ path: it.path, headers: it.headers, body: it.body }, null, 2),
    }))
  } catch (e) {
    resetOut()
    msg.value = '查询失败：' + errText(e)
  }
  busy.value = ''
}

async function create() {
  busy.value = 'create'
  resetOut()
  try {
    const j = await apiJson<{ id: string; url: string; usage?: string }>('/tools/webhook', {
      body: { action: 'create' },
    })
    whId.value = j.id
    created.value = { url: j.url, usage: j.usage || '' }
    await listReq(j.id) // 创建后立即查看一次（旧版行为），listReq 会覆盖输出区
  } catch (e) {
    resetOut()
    msg.value = '创建失败：' + errText(e)
    busy.value = ''
  }
}

function onView() {
  const id = whId.value.trim()
  if (!id) { resetOut(); msg.value = '先创建收集器，或粘贴已有 Webhook ID。'; return }
  listReq(id)
}

async function onClear() {
  const id = whId.value.trim()
  if (!id) { resetOut(); msg.value = '先创建收集器。'; return }
  resetOut()
  try {
    const j = await apiJson<{ cleared?: number }>('/tools/webhook', {
      body: { action: 'clear', id },
    })
    msg.value = '已清空 ' + (j.cleared || 0) + ' 条记录。'
  } catch (e) {
    msg.value = '清空失败：' + errText(e)
  }
}
</script>

<template>
  <p class="tool-intro">调试第三方回调的利器：点「创建收集器」得到一个专属地址，把它填到任意需要回调的地方，随后用「查看请求」抓取收到的全部请求（方法 / 头 / 体）。记录保留 <b>24 小时</b>，自动清理。</p>
  <div class="tool-bar">
    <button class="btn tool-run" :disabled="busy !== ''" @click="create">① 创建收集器</button>
    <input v-model="whId" placeholder="Webhook ID（创建后自动填入）" style="flex:1;min-width:160px;background:var(--card2);color:var(--text);border:1px solid var(--border);border-radius:10px;padding:8px 12px;font-family:var(--mono);">
    <button class="btn tool-run" :disabled="busy !== ''" @click="onView">② 查看请求</button>
    <button class="btn" @click="onClear">清空记录</button>
  </div>
  <div class="tool-result">
    <div v-if="busy === 'create'" class="tool-loading">创建中…</div>
    <div v-else-if="busy === 'list'" class="tool-loading">查询中…</div>
    <template v-else>
      <div v-if="created" class="tool-ai-box">
        <b>收集器已就绪</b>
        <div class="pg-hrow" style="margin-top:8px;">
          <span style="user-select:all;color:var(--accent);font-family:var(--mono);font-size:12px;">{{ created.url }}</span>
          <CopyBtn :text="created.url" label="复制地址" />
        </div>
        <div style="font-size:12px;color:var(--muted);margin-top:6px;">{{ created.usage }}</div>
      </div>
      <template v-if="items.length">
        <div class="tool-status" style="margin-bottom:8px;">共 {{ total }} 条 · 展示最近 {{ items.length }} 条</div>
        <div v-for="(it, i) in items" :key="i" class="tool-ai-box" style="margin-bottom:10px;">
          <b>{{ it.method }} · {{ it.time }}</b>
          <pre style="font-family:var(--mono);font-size:11.5px;white-space:pre-wrap;word-break:break-all;margin:6px 0 0;color:var(--muted);">{{ it.json }}</pre>
        </div>
      </template>
      <div v-else-if="noReqUrl" class="tool-empty">
        还没有收到请求。地址：<code style="font-family:var(--mono);color:var(--accent);user-select:all;">{{ noReqUrl }}</code> —— 用 curl 或浏览器随便发一个请求试试。
      </div>
      <div v-else-if="msg" class="tool-empty">{{ msg }}</div>
    </template>
  </div>
</template>
