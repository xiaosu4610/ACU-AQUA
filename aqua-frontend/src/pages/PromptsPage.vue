<script setup lang="ts">
/* 提示词工坊（体验中心）：自旧版 PW_CATS/PW_FALLBACK/pw* 系列函数平移 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { apiJson } from '@/composables/useApi'
import CopyBtn from '@/components/CopyBtn.vue'
import AuthBanner from '@/components/AuthBanner.vue'

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
const loaded = ref(false)
const openFull = ref<Record<string, boolean>>({})

/* pwRender 平移：分类 tab + 关键词搜索（名称/描述/全文） */
const filtered = computed(() => {
  const q = search.value.toLowerCase().trim()
  return data.value.filter(p => {
    if (cat.value !== 'all' && p.category !== cat.value) return false
    if (!q) return true
    return (p.name + p.desc + p.prompt).toLowerCase().includes(q)
  })
})

/* pwLoad 平移：GET /v1/tools/prompts，失败回退离线兜底 */
async function pwLoad() {
  try {
    const j = await apiJson<{ prompts?: PwPrompt[] }>('/tools/prompts')
    const arr = j?.prompts || []
    if (!arr.length) throw new Error('empty')
    data.value = arr
    loaded.value = true
  } catch {
    data.value = PW_FALLBACK
  }
}

/* pwSeedPlayground 平移：暂存提示词后跳转 AI 对话，PlaygroundPage 挂载时自动填入输入框 */
function pwSeedPlayground(prompt: string, name: string) {
  try { sessionStorage.setItem('aqua_pg_seed', JSON.stringify({ prompt, name })) } catch { /* 忽略 */ }
  router.push('/playground')
}

function toggleFull(id: string) {
  openFull.value[id] = !openFull.value[id]
}

onMounted(() => {
  if (!loaded.value) pwLoad()
})
</script>

<template>
  <section class="route-page">
    <div class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>体验中心</h1>
      <p>登录账号后即可对话、玩 AI 对弈游戏、用免费实用工具——所有体验与真实 API 行为完全一致。</p>
    </div>
    <AuthBanner />
    <nav class="hub-subnav" aria-label="体验中心子导航">
      <router-link class="hub-tab" to="/playground" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>AI 对话</router-link>
      <router-link class="hub-tab" to="/treehole" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22c4-3 8-6.5 8-11a8 8 0 1 0-16 0c0 4.5 4 8 8 11z" style="display:none"/><path d="M12 3c-4.4 0-8 3.6-8 8 0 4.4 3.6 8 8 8h.5c.3 0 .5.2.5.5V21l3.8-2.6C19.9 17 20.5 14 20.5 11 20.5 6.6 16.4 3 12 3z"/><circle cx="8.5" cy="10.5" r=".8" fill="currentColor"/><circle cx="12" cy="10.5" r=".8" fill="currentColor"/><circle cx="15.5" cy="10.5" r=".8" fill="currentColor"/></svg></span>树洞</router-link>
      <router-link class="hub-tab" to="/tools" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>工具箱</router-link>
      <router-link class="hub-tab" to="/prompts" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/></svg></span>提示词工坊</router-link>
    </nav>

    <div class="pw-wrap">
      <p class="hub-desc">智能体人格与高质量 Skill 收录——每条都可复制、可一键带去 AI 对话试跑，也开放 <code>GET /v1/tools/prompts</code> 供程序化调用。投稿你的提示词请来 <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener" style="color:var(--accent);">QQ 频道</a>。</p>
      <div class="pw-toolbar">
        <div class="pw-cats" id="pw-cats">
          <button v-for="c in PW_CATS" :key="c.id" class="pill" :class="{ active: cat === c.id }" :data-pw-cat="c.id" type="button" @click="cat = c.id">{{ c.label }}</button>
        </div>
        <input id="pw-search" v-model="search" placeholder="搜索提示词…" />
      </div>
      <div class="pw-grid" id="pw-grid">
        <div v-if="!filtered.length" class="model-empty">没有匹配的提示词</div>
        <div v-for="p in filtered" :key="p.id" class="pw-card">
          <div class="pw-card-head">
            <b>{{ p.name }}</b>
            <span v-if="p.official" class="pw-official">官方收录</span>
            <span class="pw-cat-tag">{{ PW_CAT_LABEL[p.category] || p.category }}</span>
          </div>
          <p>{{ p.desc }}</p>
          <div class="pw-card-meta">建议模型：<code>{{ p.model_hint || '任意 chat 模型' }}</code></div>
          <div class="pw-card-btns">
            <CopyBtn :text="p.prompt" label="复制提示词" />
            <button class="btn" type="button" @click="pwSeedPlayground(p.prompt, p.name)">去 AI 对话试跑</button>
            <button class="btn" type="button" @click="toggleFull(p.id)">{{ openFull[p.id] ? '收起全文' : '展开全文' }}</button>
          </div>
          <pre v-show="openFull[p.id]" class="pw-full">{{ p.prompt }}</pre>
        </div>
      </div>
    </div>
  </section>
</template>
