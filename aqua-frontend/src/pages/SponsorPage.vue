<script setup lang="ts">
// 赞助页：收款码 + 去向说明 + 免费支持方式（文案与旧版一致）
import AqIcon from '@/components/AqIcon.vue'
</script>

<template>
  <section class="route-page">
    <div class="sponsor-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="#f43f5e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/></svg></span>赞助 AQUA</h1>
      <p>AQUA 是 ACU 工程系列的开源旗舰项目——注册免费、密钥自助创建、账号制长期运行。但服务器、带宽与上游算力都是每月真实支出的成本。如果 AQUA 帮你的项目省了钱、省了心，欢迎请作者喝杯咖啡，这是对我们最直接的支持。</p>
    </div>

    <div class="sponsor-grid">
      <div class="donate-card">
        <span class="donate-tag wx"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M8.69 3.5C4.98 3.5 2 6.05 2 9.2c0 1.77.94 3.35 2.4 4.39l-.6 1.8 2.1-1.05c.6.17 1.24.28 1.91.3-.12-.42-.18-.86-.18-1.31 0-2.98 2.98-5.4 6.65-5.4.23 0 .45.01.67.03C14.32 5.35 11.75 3.5 8.69 3.5zM6.6 7.3a.85.85 0 1 1 0 1.7.85.85 0 0 1 0-1.7zm4.2 0a.85.85 0 1 1 0 1.7.85.85 0 0 1 0-1.7zM15.4 9.2c-3.22 0-5.8 2.06-5.8 4.6 0 2.54 2.58 4.6 5.8 4.6.55 0 1.08-.06 1.58-.18l1.85.92-.53-1.58c1.28-.86 2.1-2.17 2.1-3.76 0-2.54-2.58-4.6-5.8-4.6zm-2.1 2.2a.75.75 0 1 1 0 1.5.75.75 0 0 1 0-1.5zm4 0a.75.75 0 1 1 0 1.5.75.75 0 0 1 0-1.5z"/></svg>微信收款码</span>
        <a href="/sponsor-wechat.jpg" target="_blank" rel="noopener" title="点击查看大图"><img src="/sponsor-wechat.jpg" alt="微信收款码" loading="lazy" decoding="async"></a>
        <div class="sponsor-hint">打开微信「扫一扫」→ 识别二维码 → 金额随意</div>
      </div>
      <div class="donate-card">
        <span class="donate-tag ali"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm6.42 13.35c-.32.86-1.24 1.42-2.44 1.42-1.9 0-4.05-.9-5.98-2.1a29.16 29.16 0 0 1-4.36 2.06l-.4-.96a27.6 27.6 0 0 0 3.98-1.94 21.5 21.5 0 0 1-2.5-2.1l.75-.7s1.1 1.06 2.68 2.14c.9-1.72 1.5-3.6 1.72-5.17H8.1V7.2h3.16V5.6h1.9v1.6h3.2v.8h-3.2v1.05s2.62-.05 3.9.28c-.2 1.9-.86 4.06-1.8 5.98 1.34-.62 2.6-1.3 3.56-1.96-.9 1.28-1.98 2.52-3.16 3.6 1.66.83 3.34 1.38 4.76 1.38z"/></svg>支付宝收款码</span>
        <a href="/sponsor-alipay.jpg" target="_blank" rel="noopener" title="点击查看大图"><img src="/sponsor-alipay.jpg" alt="支付宝收款码" loading="lazy" decoding="async"></a>
        <div class="sponsor-hint">打开支付宝「扫一扫」→ 识别二维码 → 金额随意</div>
      </div>
    </div>

    <section class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/></svg></span>你的赞助会用在哪里</h2>
      <div class="sponsor-why">
        <div class="why-card">
          <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><path d="M6 6h.01M6 18h.01"/></svg></span>
          <b>服务器与带宽</b>
          <span>国内服务器 + 隧道链路的租用与维护，让每个接口都快且稳。</span>
        </div>
        <div class="why-card">
          <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3"/></svg></span>
          <b>上游算力成本</b>
          <span>官方自营通道与密钥池的持续供给，兜底 Auto 智能路由的可用性。</span>
        </div>
        <div class="why-card">
          <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/></svg></span>
          <b>持续开发</b>
          <span>新功能、新工具与更多 ACU 生态项目，都靠你的支持推进。</span>
        </div>
      </div>
    </section>

    <section class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg></span>不花钱也一样支持</h2>
      <div class="sponsor-alt">
        <a class="badge" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="text-decoration:none;"><AqIcon name="star" :size="14" /> Gitee 点个 Star</a>
        <a class="badge" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="text-decoration:none;"><AqIcon name="star" :size="14" /> GitHub 点个 Star</a>
        <a class="badge" href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener" style="text-decoration:none;">加入 QQ 频道</a>
        <a class="badge" href="https://qm.qq.com/q/qoe6XbsVge" target="_blank" rel="noopener" style="text-decoration:none;">加入 QQ 一群 1103667832</a>
        <a class="badge" href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener" style="text-decoration:none;">加入 QQ 二群 1006740220</a>
      </div>
      <p class="sponsor-note">赞助完全自愿、金额随意，<b>不与任何功能挂钩</b>——AQUA 注册免费，登录后在控制台自助创建密钥即可使用。感谢每一位支持者，你们让免费 AI 网关走得更远 <AqIcon name="heart" :size="14" style="color:#f43f5e;" /></p>
    </section>
  </section>
</template>
