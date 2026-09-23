<script setup lang="ts">
/* 社区页：QQ 一群 / 二群 / 频道入群入口（群号/链接来自 useMeta 的 /v1/meta 配置，缺省兜底页脚同源信息）+ 二维码 */
import { computed, onMounted, ref } from 'vue'
import { useMeta } from '@/composables/useMeta'
import { copyText } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'

interface GroupInfo { name: string; no: string; url: string; desc: string }

const { meta, loadMeta } = useMeta()
onMounted(() => { loadMeta() })

/* /v1/meta 下发 qq_group / qq_group_url / qq_group2 / qq_group_url2，缺省回退内置兜底（与页脚同源）
 * 顺序（20260919 站长定稿）：新群优先靠前——二群 → 一群；频道作为独立补充入口 */
const groups = computed<GroupInfo[]>(() => [
  { name: 'QQ 二群（新群 · 优先加入）', no: meta.value?.qq_group2 || '1006740220', url: meta.value?.qq_group_url2 || 'https://qm.qq.com/q/o8QDbza2Ge', desc: '新群入口 · 官方活动与技术交流' },
  { name: 'QQ 一群', no: meta.value?.qq_group || '1103667832', url: meta.value?.qq_group_url || 'https://qm.qq.com/q/qoe6XbsVge', desc: '主群入口 · 技术交流与问题反馈' },
])
const channel = computed(() => ({ name: 'QQ 频道', no: 'pd57362562', url: 'https://pd.qq.com/s/e4ktxw1b8' }))
const cards = computed<GroupInfo[]>(() => groups.value)

/* 二维码：入群链接生成；加载失败自动隐藏（保留链接 + 复制群号入口） */
function qrSrc(url: string): string {
  return 'https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=' + encodeURIComponent(url)
}
const qrFail = ref<Record<string, boolean>>({})

const copied = ref('')
async function copyNo(no: string) {
  if (await copyText(no)) {
    copied.value = no
    setTimeout(() => { copied.value = '' }, 1500)
  }
}
</script>

<template>
  <div class="wrap">
    <div class="page-head fade-up">
      <div>
        <h1><AqIcon name="chat" />加入官方 Q 群</h1>
        <div class="sub">站点以公益为主——加群可获取活动、折扣与故障通知，也方便交流使用问题。<b>鼓励按需加入多个群</b>；入群请备注「模型站用户」。</div>
      </div>
    </div>

    <div class="grid3 fade-up">
      <div v-for="(g, i) in cards" :key="g.no" class="card hoverable comm-card">
        <span class="tag acc">{{ g.name }}</span>
        <div v-if="!qrFail[g.url]" class="qr-box">
          <img :src="qrSrc(g.url)" :alt="g.name + ' 二维码'" loading="lazy" decoding="async" @error="qrFail[g.url] = true">
        </div>
        <div class="cc-no">{{ g.no }}</div>
        <p class="dim" style="margin: 0; font-size: 12.5px;">{{ g.desc }}</p>
        <div class="cc-ops">
          <a class="btn primary" :href="g.url" target="_blank" rel="noopener">
            <AqIcon name="external" :size="14" />一键加群
          </a>
          <button class="btn sm" @click="copyNo(g.no)">
            <AqIcon :name="copied === g.no ? 'check' : 'copy'" :size="13" />{{ copied === g.no ? '已复制' : '复制群号' }}
          </button>
        </div>
      </div>
    </div>

    <section class="channel-block fade-up">
      <div>
        <span class="tag">补充入口</span>
        <h2>{{ channel.name }}</h2>
        <p class="dim">公告聚合与话题讨论，作为 QQ 群之外的补充渠道。</p>
      </div>
      <a class="btn" :href="channel.url" target="_blank" rel="noopener"><AqIcon name="external" :size="14" />进入频道</a>
    </section>
    <p class="dim fade-up" style="text-align: center; font-size: 12.5px; margin-top: 18px;">
      加群链接如失效，可搜索群号手动加入；是否加入多个群由你决定。
    </p>
  </div>
</template>

<style scoped>
.comm-card { display: flex; flex-direction: column; align-items: center; gap: 10px; text-align: center; }
.qr-box { width: 150px; height: 150px; padding: 8px; border: 1px solid var(--line); border-radius: var(--r-md); background: var(--bg1); }
.qr-box img { width: 100%; height: 100%; }
.cc-no { font-size: 28px; font-weight: 700; font-family: var(--mono); font-variant-numeric: tabular-nums; letter-spacing: .02em; color: var(--txt0); }
.cc-ops { display: flex; flex-direction: column; gap: 8px; align-items: stretch; width: 100%; max-width: 200px; margin-top: 2px; }
.channel-block { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin-top: 28px; padding: 22px 24px; border-top: 1px solid var(--line-strong); border-bottom: 1px solid var(--line-strong); }
.channel-block h2 { margin-top: 8px; font-size: 20px; }
.channel-block p { margin-top: 6px; font-size: 13px; }
@media (max-width: 640px) { .channel-block { align-items: flex-start; flex-direction: column; padding: 20px 8px; } }
</style>
