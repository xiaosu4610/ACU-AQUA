<script setup lang="ts">
/* 社区页：QQ 一群 / 二群 / 频道入群入口（数据来自 /v1/meta 配置，缺省兜底页脚同源信息） */
import { onMounted, ref } from 'vue'
import { copyText } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'

interface GroupInfo { name: string; no: string; url: string; desc: string }

const groups = ref<GroupInfo[]>([])
const channel = ref({ name: 'QQ 频道', no: 'pd57362562', url: 'https://pd.qq.com/s/e4ktxw1b8' })
const copied = ref('')

onMounted(async () => {
  const fallback: GroupInfo[] = [
    { name: 'QQ 一群', no: '1103667832', url: 'https://qm.qq.com/q/qoe6XbsVge', desc: '主群 · 人数较多，优先加入' },
    { name: 'QQ 二群', no: '1006740220', url: 'https://qm.qq.com/q/o8QDbza2Ge', desc: '满员分流群 · 主群加不进再进二群' },
  ]
  const fallbackChannel = { name: 'QQ 频道', no: 'pd57362562', url: 'https://pd.qq.com/s/e4ktxw1b8' }
  try {
    const r = await fetch('/v1/meta')
    const j = await r.json()
    groups.value = [
      { name: 'QQ 一群', no: j.qq_group || fallback[0].no, url: j.qq_group_url || fallback[0].url, desc: fallback[0].desc },
      { name: 'QQ 二群', no: j.qq_group2 || fallback[1].no, url: j.qq_group_url2 || fallback[1].url, desc: fallback[1].desc },
    ]
  } catch {
    groups.value = fallback
  }
  if (!groups.value.length) groups.value = fallback
})

async function copyNo(no: string) {
  if (await copyText(no)) {
    copied.value = no
    setTimeout(() => { copied.value = '' }, 1500)
  }
}
</script>

<template>
  <section class="route-page">
    <div class="comm-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/></svg></span>加入官方 Q 群</h1>
      <p>提问、反馈、交流与第一时间获取活动通知——入群请备注「模型站用户」。</p>
    </div>

    <div class="comm-grid">
      <div v-for="(g, i) in groups" :key="g.no" class="comm-card" :class="{ alt: i === 1 }">
        <div class="cc-badge">{{ g.name }}</div>
        <div class="cc-no">{{ g.no }}</div>
        <p class="cc-desc">{{ g.desc }}</p>
        <div class="cc-ops">
          <a class="btn tool-run" :href="g.url" target="_blank" rel="noopener">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><path d="M15 3h6v6"/><path d="M10 14 21 3"/></svg>
            一键加群
          </a>
          <button class="mini-btn" @click="copyNo(g.no)">
            <AqIcon :name="copied === g.no ? 'check' : 'copy'" :size="12" /> {{ copied === g.no ? '已复制群号' : '复制群号' }}
          </button>
        </div>
      </div>

      <div class="comm-card channel">
        <div class="cc-badge">{{ channel.name }}</div>
        <div class="cc-no">{{ channel.no }}</div>
        <p class="cc-desc">官方 QQ 频道 · 话题讨论与公告聚合</p>
        <div class="cc-ops">
          <a class="btn tool-run" :href="channel.url" target="_blank" rel="noopener">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><path d="M15 3h6v6"/><path d="M10 14 21 3"/></svg>
            进入频道
          </a>
          <button class="mini-btn" @click="copyNo(channel.no)">
            <AqIcon :name="copied === channel.no ? 'check' : 'copy'" :size="12" /> {{ copied === channel.no ? '已复制' : '复制频道号' }}
          </button>
        </div>
      </div>
    </div>

    <p class="comm-hint">加群链接长期有效；如链接失效，可搜索群号手动加入。群内禁止发广告与违规内容。</p>
  </section>
</template>

<style scoped>
.comm-head { margin-bottom: 18px; }
.comm-head h1 { display: flex; align-items: center; gap: 10px; font-size: 24px; margin: 0 0 6px; }
.comm-head .ic svg { width: 26px; height: 26px; color: var(--aqua, #38bdf8); }
.comm-head p { color: var(--muted, #8a94a6); font-size: 13.5px; margin: 0; }
.comm-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
.comm-card { background: linear-gradient(165deg, var(--card), var(--card2, var(--card))); border: 1px solid var(--border); border-radius: 18px; padding: 26px 22px; display: flex; flex-direction: column; align-items: center; gap: 10px; text-align: center; transition: transform .15s, border-color .15s, box-shadow .15s; }
.comm-card:hover { transform: translateY(-3px); border-color: var(--aqua, #38bdf8); box-shadow: 0 10px 28px rgba(0,0,0,.2); }
.comm-card.alt { border-color: rgba(129,140,248,.4); }
.comm-card.channel { border-color: rgba(52,211,153,.35); }
.cc-badge { font-size: 13px; font-weight: 800; letter-spacing: .06em; color: var(--aqua, #38bdf8); background: rgba(56,189,248,.12); border: 1px solid rgba(56,189,248,.35); border-radius: 999px; padding: 4px 16px; }
.comm-card.alt .cc-badge { color: #818cf8; background: rgba(129,140,248,.12); border-color: rgba(129,140,248,.35); }
.comm-card.channel .cc-badge { color: #34d399; background: rgba(52,211,153,.12); border-color: rgba(52,211,153,.35); }
.cc-no { font-size: 32px; font-weight: 800; letter-spacing: .02em; font-variant-numeric: tabular-nums; }
.cc-desc { margin: 0; font-size: 12.5px; color: var(--muted, #8a94a6); }
.cc-ops { display: flex; flex-direction: column; gap: 8px; align-items: center; margin-top: 4px; }
.cc-ops .btn { display: inline-flex; align-items: center; gap: 7px; text-decoration: none; }
.cc-ops .btn svg { width: 15px; height: 15px; }
.comm-hint { text-align: center; color: var(--muted, #8a94a6); font-size: 12.5px; margin-top: 18px; }
@media (max-width: 860px) {
  .comm-grid { grid-template-columns: 1fr; }
}
</style>
