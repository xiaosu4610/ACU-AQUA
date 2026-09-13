<script setup lang="ts">
/* 提示词工坊：接口对接 1:1 平移自旧版
 * （GET /tools/prompts 官方清单 + 失败回退离线精选 + 一键带去 AI 对话试跑 aqua_pg_seed）
 * UI 全新：.chips 分类筛选 + 关键词搜索 + .grid2 卡片（标题/正文预览/CopyBtn/展开全文） */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { apiJson } from '@/composables/useApi'
import CopyBtn from '@/components/CopyBtn.vue'
import AqIcon from '@/components/AqIcon.vue'

interface PwPrompt {
  id: string
  name: string
  desc: string
  category: string
  official: boolean
  model_hint?: string
  prompt: string
}

const PW_CATS = [
  { id: 'all', label: '全部' }, { id: 'agent', label: '智能体人格' }, { id: 'work', label: '职场' },
  { id: 'coding', label: '编程' }, { id: 'writing', label: '写作' }, { id: 'study', label: '学习' }, { id: 'life', label: '生活' },
]
const PW_CAT_LABEL: Record<string, string> = {}
PW_CATS.forEach(c => { PW_CAT_LABEL[c.id] = c.label })

/* 加载失败时的离线兜底（完整版以网关 GET /v1/tools/prompts 为准） */
const PW_FALLBACK: PwPrompt[] = [
  { id: 'fb-weekly', name: '周报炼金师', desc: '把零散的工作碎片炼成结构化、有亮点的周报', category: 'work', official: false, model_hint: 'qwen3-8b',
    prompt: '你是周报炼金师。我会给你本周做过的零散事项。请输出：1. 本周核心产出（3-5 条，动词开头，突出结果与数据）；2. 进行中事项（进度与下一步）；3. 风险与需要的支持；4. 下周计划。克制具体，不写空话，缺失信息用【待补充】标注。' },
  { id: 'fb-regex', name: '正则魔法师', desc: '描述就生成正则，正则就翻译成人话', category: 'coding', official: false, model_hint: 'qwen3-8b',
    prompt: '你是正则魔法师。我给「需求描述」时：生成正则，给 5 个匹配示例、3 个不匹配示例，并逐段解释结构；我给「正则表达式」时：逐段翻译成人话，列出易误匹配场景。标注 JS/Python/Java 方言差异。' },
  { id: 'fb-feynman', name: '费曼学习教练', desc: '用教别人的方式逼你真正学懂一个概念', category: 'study', official: false, model_hint: 'qwen3-8b',
    prompt: '你是费曼学习教练。我说一个概念，你不直接讲解：1. 先让我用自己的话解释；2. 针对模糊与跳步处一次只追问一个问题；3. 我说懂了就出反例检验；4. 掌握后用 100 字大白话总结并指出易混淆的相邻概念。全程苏格拉底式追问。' },
]

const router = useRouter()
const cat = ref('all')
const search = ref('')
const data = ref<PwPrompt[]>([])
const loading = ref(true)
const offline = ref(false)
const openFull = ref<Record<string, boolean>>({})

/* 分类 tab + 关键词搜索（名称/描述/全文） */
const filtered = computed(() => {
  const q = search.value.toLowerCase().trim()
  return data.value.filter(p => {
    if (cat.value !== 'all' && p.category !== cat.value) return false
    if (!q) return true
    return (p.name + p.desc + p.prompt).toLowerCase().includes(q)
  })
})

/* 加载：GET /v1/tools/prompts，失败回退离线兜底 */
async function load() {
  loading.value = true
  offline.value = false
  try {
    const j = await apiJson<{ prompts?: PwPrompt[] }>('/tools/prompts')
    const arr = j?.prompts || []
    if (!arr.length) throw new Error('empty')
    data.value = arr
  } catch {
    data.value = PW_FALLBACK
    offline.value = true
  } finally {
    loading.value = false
  }
}

/* 暂存提示词后跳转 AI 对话，PlaygroundPage 挂载时自动填入输入框 */
function seedPlayground(prompt: string, name: string) {
  try { sessionStorage.setItem('aqua_pg_seed', JSON.stringify({ prompt, name })) } catch { /* 忽略 */ }
  router.push('/playground')
}

function toggleFull(id: string) {
  openFull.value[id] = !openFull.value[id]
}

onMounted(() => {
  load()
})
</script>

<template>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="bulb" :size="22" />提示词工坊</h1>
        <div class="sub">智能体人格与高质量 Skill 收录——每条都可复制、可一键带去 AI 对话试跑，也开放 <code>GET /v1/tools/prompts</code> 供程序化调用。</div>
      </div>
      <div class="ops">
        <a class="btn" href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener"><AqIcon name="message" :size="14" />投稿提示词</a>
      </div>
    </div>

    <!-- 筛选工具条 -->
    <div class="card fade-up">
      <div class="pw-bar">
        <div class="chips">
          <button v-for="c in PW_CATS" :key="c.id" class="chip" :class="{ on: cat === c.id }" type="button" @click="cat = c.id">{{ c.label }}</button>
        </div>
        <input v-model="search" class="input pw-search" placeholder="搜索提示词…" />
      </div>
    </div>

    <!-- 离线兜底提示 -->
    <div v-if="offline && !loading" class="banner warn mt12">
      <AqIcon name="alert" :size="15" />网关数据暂时没连上，当前展示离线精选——完整清单以 GET /v1/tools/prompts 为准。
    </div>

    <!-- 加载态 -->
    <div v-if="loading" class="grid2 mt16">
      <div v-for="i in 4" :key="i" class="card">
        <div class="skeleton" style="width: 40%; height: 18px;" />
        <div class="skeleton mt12" style="height: 13px; width: 70%;" />
        <div class="skeleton mt8" style="height: 76px;" />
      </div>
    </div>

    <!-- 空态 -->
    <div v-else-if="!filtered.length" class="card mt16">
      <div class="empty">
        <div class="big"><AqIcon name="bulb" :size="38" /></div>
        <b>没有匹配的提示词</b>
        <div class="dim">换个分类或关键词试试。</div>
      </div>
    </div>

    <!-- 提示词卡片 -->
    <div v-else class="grid2 mt16">
      <div v-for="p in filtered" :key="p.id" class="card hoverable">
        <div class="row between">
          <b>{{ p.name }}</b>
          <span v-if="p.official" class="tag grad">官方收录</span>
        </div>
        <div class="row wrap mt8" style="gap: 6px;">
          <span class="tag acc">{{ PW_CAT_LABEL[p.category] || p.category }}</span>
          <span class="tag">建议模型 · {{ p.model_hint || '任意 chat 模型' }}</span>
        </div>
        <p class="dim mt12">{{ p.desc }}</p>
        <!-- 正文预览（3 行截断），展开后显示全文 -->
        <pre v-if="openFull[p.id]" class="code pw-full">{{ p.prompt }}</pre>
        <pre v-else class="code pw-preview">{{ p.prompt }}</pre>
        <div class="row between mt12">
          <CopyBtn :text="p.prompt" label="复制提示词" size="sm" />
          <div class="row">
            <button class="btn sm" type="button" @click="toggleFull(p.id)">{{ openFull[p.id] ? '收起全文' : '展开全文' }}</button>
            <button class="btn sm" type="button" @click="seedPlayground(p.prompt, p.name)"><AqIcon name="send" :size="13" />去 AI 对话试跑</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：工具条排布 / 正文预览截断高度，颜色全部走设计令牌 */
.pw-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.pw-search { max-width: 260px; }
.pw-preview { white-space: pre-wrap; word-break: break-word; max-height: 5.1em; overflow: hidden; }
.pw-full { white-space: pre-wrap; word-break: break-word; max-height: 340px; overflow-y: auto; }
@media (max-width: 640px) {
  .pw-search { max-width: 100%; }
}
</style>
