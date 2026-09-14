<script setup lang="ts">
/* 赞助页：左栏 赞助说明 + 去向 + 免费支持方式，右栏 赞赏码卡（收款码与文案与旧版一致） */
import AqIcon from '@/components/AqIcon.vue'

const WHY = [
  { icon: 'server', t: '服务器与带宽', d: '国内服务器 + 隧道链路的租用与维护，让每个接口都快且稳。' },
  { icon: 'bolt', t: '上游算力成本', d: '官方自营通道与密钥池的持续供给，兜底 Auto 智能路由的可用性。' },
  { icon: 'bulb', t: '持续开发', d: '新功能、新工具与更多 ACU 生态项目，都靠你的支持推进。' },
]

const FREE = [
  { label: 'Gitee 点个 Star', icon: 'star', url: 'https://gitee.com/xiaosu4610/aqua-rust-workers' },
  { label: 'GitHub 点个 Star', icon: 'star', url: 'https://github.com/xiaosu4610/aqua-rust-workers' },
  { label: '加入 QQ 频道', icon: 'chat', url: 'https://pd.qq.com/s/e4ktxw1b8' },
  { label: '加入 QQ 一群 1103667832', icon: 'chat', url: 'https://qm.qq.com/q/qoe6XbsVge' },
  { label: '加入 QQ 二群 1006740220', icon: 'chat', url: 'https://qm.qq.com/q/o8QDbza2Ge' },
]
</script>

<template>
  <div class="wrap">
    <div class="page-head fade-up">
      <div>
        <h1><AqIcon name="heart" style="color: var(--bad);" />赞助 AQUA api</h1>
        <div class="sub">AQUA api 是 ACU 工程系列的开源旗舰项目——注册免费、密钥自助创建、账号制长期运行。但服务器、带宽与上游算力都是每月真实支出的成本。如果 AQUA api 帮你的项目省了钱、省了心，欢迎请作者喝杯咖啡，这是对我们最直接的支持。</div>
      </div>
    </div>

    <div class="sponsor-grid fade-up">
      <!-- 左栏：赞助说明 + 去向 + 免费支持方式 -->
      <div class="out-pane" style="display: flex; flex-direction: column; gap: 16px;">
        <div class="card">
          <div class="card-h"><AqIcon name="heart" :size="15" style="color: var(--bad);" />你的赞助会用在哪里</div>
          <div class="mt12">
            <div v-for="w in WHY" :key="w.t" class="why-item">
              <span class="why-ic"><AqIcon :name="w.icon" :size="16" /></span>
              <div>
                <b>{{ w.t }}</b>
                <div class="dim" style="font-size: 13px;">{{ w.d }}</div>
              </div>
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-h"><AqIcon name="user" :size="15" />不花钱也一样支持</div>
          <div class="chips mt12">
            <a v-for="f in FREE" :key="f.label" class="chip" :href="f.url" target="_blank" rel="noopener" style="text-decoration: none;">
              <AqIcon :name="f.icon" :size="13" />{{ f.label }}
            </a>
          </div>
          <p class="dim mt12" style="font-size: 13px;">
            赞助完全自愿、金额随意，<b>不与任何功能挂钩</b>——AQUA api 注册免费，登录后在控制台自助创建密钥即可使用。
            感谢每一位支持者，你们让免费 AI 网关走得更远 <AqIcon name="heart" :size="13" style="color: var(--bad);" />
          </p>
        </div>
      </div>

      <!-- 右栏：赞赏码卡 -->
      <div class="card accent donate-card">
        <div class="card-h"><AqIcon name="qr" :size="15" />扫码赞赏</div>
        <div class="donate-item mt12">
          <span class="donate-tag"><AqIcon name="chat" :size="13" />微信收款码</span>
          <a href="/sponsor-wechat.jpg" target="_blank" rel="noopener" title="点击查看大图">
            <img src="/sponsor-wechat.jpg" alt="微信收款码" loading="lazy" decoding="async">
          </a>
          <div class="dim" style="font-size: 12px; text-align: center;">打开微信「扫一扫」→ 识别二维码 → 金额随意</div>
        </div>
        <hr>
        <div class="donate-item">
          <span class="donate-tag"><AqIcon name="coin" :size="13" />支付宝收款码</span>
          <a href="/sponsor-alipay.jpg" target="_blank" rel="noopener" title="点击查看大图">
            <img src="/sponsor-alipay.jpg" alt="支付宝收款码" loading="lazy" decoding="async">
          </a>
          <div class="dim" style="font-size: 12px; text-align: center;">打开支付宝「扫一扫」→ 识别二维码 → 金额随意</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sponsor-grid { display: grid; grid-template-columns: 1.2fr 1fr; gap: 16px; align-items: start; }
@media (max-width: 880px) { .sponsor-grid { grid-template-columns: 1fr; } }
.why-item { display: flex; gap: 12px; align-items: flex-start; padding: 11px 0; border-bottom: 1px solid var(--line); }
.why-item:last-child { border-bottom: 0; padding-bottom: 0; }
.why-ic { width: 34px; height: 34px; flex: none; border-radius: 10px; background: var(--acc-soft); color: var(--acc); display: flex; align-items: center; justify-content: center; }
.donate-card { display: flex; flex-direction: column; }
.donate-item { display: flex; flex-direction: column; gap: 8px; align-items: center; }
.donate-tag { display: inline-flex; align-items: center; gap: 6px; padding: 3px 12px; border-radius: 99px; font-size: 12px; font-weight: 700; background: var(--acc-soft); color: var(--acc); }
.donate-item img { width: 100%; max-width: 220px; border-radius: var(--r-md); border: 1px solid var(--line); }
</style>
