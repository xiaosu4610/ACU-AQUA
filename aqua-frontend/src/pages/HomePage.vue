<script setup lang="ts">
import { computed, ref } from 'vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { GATEWAY } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'

const baseUrl = GATEWAY.startsWith('/') ? location.origin + GATEWAY : GATEWAY
</script>

<template>
  <section>
    <header class="home-hero">
      <div class="logo">
        <img src="/favicon.ico" alt="AQUA" width="60" height="60" style="border-radius:14px;" class="hero-logo">
        <span class="brand grad-flow">AQUA</span>
      </div>
      <p class="tagline">免费 · 极速 · 注册即用 —— 一个接口接入 Nvidia NIM 与官方自营专线的 OpenAI 兼容 API 网关（ACU 工程系列旗舰项目）</p>
      <!-- CTA 主行动区：注册/登录入口第一眼可见 -->
      <div class="hero-cta">
        <router-link v-if="!isLoggedIn()" class="cta-main" to="/login">注册 / 登录 · 创建密钥</router-link>
        <router-link v-else class="cta-main" to="/console">进入我的控制台</router-link>
        <router-link class="cta-sub" to="/api">查看 API 文档</router-link>
      </div>
      <p class="hero-note">注册免费 · 控制台一键创建密钥 · 随时吊销重建 · 免费模型注册即用；收费模型见模型中心（aqua/ 专线，按量计费 0.2 倍率限时补贴）</p>
      <div class="badges">
        <a class="badge" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="text-decoration:none;" title="Gitee 仓库 · 去点个 Star">
          <span class="ic"><svg viewBox="0 0 24 24" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26L21.5 9.27l-4.75 4.63 1.12 6.53L12 17.77l-5.87 3.09 1.12-6.53L2.5 9.27l6.6-1.01L12 2z"/></svg></span>Gitee 仓库
        </a>
        <a class="badge" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="text-decoration:none;" title="GitHub 仓库 · 去点个 Star">
          <span class="ic"><svg viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg></span>GitHub 仓库
        </a>
      </div>
    </header>

    <!-- ===== 板块一：直达入口 ===== -->
    <section id="h-quick" class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg></span>直达入口</h2>
      <div class="quick-tiles">
        <router-link class="qtile" to="/playground">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg></span>
          <b>在线体验</b>
          <span class="qt-desc">登录后流式对话，全模型切换即开即用</span>
          <span class="qt-go">立即开聊 →</span>
        </router-link>
        <router-link class="qtile" to="/console">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg></span>
          <b>个人控制台</b>
          <span class="qt-desc">创建 / 管理 API 密钥，头像与账号设置</span>
          <span class="qt-go">进入控制台 →</span>
        </router-link>
        <router-link class="qtile" to="/usage">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/></svg></span>
          <b>我的用量</b>
          <span class="qt-desc">登录后查看用量统计与调用日志</span>
          <span class="qt-go">查一查 →</span>
        </router-link>
        <router-link class="qtile" to="/models">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg></span>
          <b>模型中心</b>
          <span class="qt-desc">模型列表 + 能力总览，实时健康分、一键复制模型 ID</span>
          <span class="qt-go">进入中心 →</span>
        </router-link>
        <router-link class="qtile" to="/tools">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>
          <b>工具箱</b>
          <span class="qt-desc">IP 定位 / 翻译 / 子网计算 / 小游戏，纯免费</span>
          <span class="qt-go">打开工具箱 →</span>
        </router-link>
        <router-link class="qtile" to="/api">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m8 7-5 5 5 5"/><path d="m16 7 5 5-5 5"/><path d="m13 4-2 16"/></svg></span>
          <b>API 文档</b>
          <span class="qt-desc">端点、参数、错误码一览，OpenAI 协议全兼容</span>
          <span class="qt-go">查阅文档 →</span>
        </router-link>
        <router-link class="qtile" to="/arena">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 2 3 6v14a2 2 0 0 0 2 2h4a2 2 0 0 0 2-2V6L8 2z"/><path d="M4 6h16"/><path d="M18 2l3 4v14a2 2 0 0 1-2 2h-4a2 2 0 0 1-2-2V6l3-4z"/></svg></span>
          <b>模型竞技场</b>
          <span class="qt-desc">双模型盲测对比，投票揭晓身份，全站胜率排行</span>
          <span class="qt-go">开始对决 →</span>
        </router-link>
        <router-link class="qtile" to="/status">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></span>
          <b>状态大屏</b>
          <span class="qt-desc">全站调用量、成功率、模型健康度实时透明</span>
          <span class="qt-go">查看大屏 →</span>
        </router-link>
        <router-link class="qtile" to="/sponsor">
          <span class="qt-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/></svg></span>
          <b>赞助支持</b>
          <span class="qt-desc">请作者喝杯咖啡，助服务器与算力走得更远</span>
          <span class="qt-go">去支持 →</span>
        </router-link>
      </div>
    </section>

    <!-- ===== 板块二：生态与合作 ===== -->
    <section id="h-eco" class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg></span>生态与合作</h2>
      <div class="friends">
        <div class="friends-title">友情链接</div>
        <div class="friends-row">
          <a class="friend-card" href="https://miofog.top/" target="_blank" rel="noopener" title="miofog公益API">
            <img src="https://miofog.top/favicon.ico" alt="miofog公益API" onerror="this.style.display='none';this.parentElement.classList.add('no-logo')">
            <span class="f-info">
              <span class="f-name">miofog公益API</span>
              <span class="f-desc">免费 AI 模型平台 · 首个基于 AQUA 开源版二开</span>
            </span>
          </a>
          <a class="friend-card" href="https://guliang.me/guliang-ai" target="_blank" rel="noopener" title="顾凉中转站">
            <img src="https://guliang.me/i/u/PNThg82.png" alt="顾凉中转站" loading="lazy" onerror="this.style.display='none'">
            <span class="f-info">
              <span class="f-name">顾凉中转站</span>
              <span class="f-desc">自研聚合中转 · 纯免费公益</span>
            </span>
          </a>
          <a class="friend-card" href="https://cloudagnetnew.nsdmc.top" target="_blank" rel="noopener" title="CloudAgent API 网关">
            <img src="https://cloudagnetnew.nsdmc.top/favicon.ico" alt="CloudAgent API 网关" loading="lazy" onerror="this.style.display='none';this.parentElement.classList.add('no-logo')">
            <span class="f-info">
              <span class="f-name">CloudAgent API 网关</span>
              <span class="f-desc">统一 API 网关 · 海量模型聚合接入</span>
            </span>
          </a>
        </div>
      </div>
    </section>

    <!-- ===== 板块三：社区与支持 ===== -->
    <section id="h-community" class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg></span>社区与支持</h2>
      <div class="home-community">
        <div class="qq-block">
          <div class="qq-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="7" r="4"/><path d="M4 21v-1a8 8 0 0 1 16 0v1"/><path d="M9 12l-2 3M15 12l2 3"/></svg>
          </div>
          <div class="qq-info">
            <b>AQUA 官方交流一群（QQ 群）</b>
            <p>技术交流、使用反馈、问题求助都在这里，公告与更新同步推送，<b>一群将满时请加二群</b></p>
            <p>群号：<span class="qq-num">1103667832</span></p>
          </div>
          <div class="qq-actions">
            <a class="qq-join" href="https://qm.qq.com/q/qoe6XbsVge" target="_blank" rel="noopener">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5"/><path d="M5 12l7-7 7 7"/></svg>
              加入群聊
            </a>
            <CopyBtn text="1103667832" label="复制群号" />
          </div>
        </div>
        <div class="qq-block">
          <div class="qq-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="7" r="4"/><path d="M4 21v-1a8 8 0 0 1 16 0v1"/><path d="M9 12l-2 3M15 12l2 3"/></svg>
          </div>
          <div class="qq-info">
            <b>AQUA 官方交流二群（QQ 群）</b>
            <p>一群满员分流新群，交流内容与一群完全一致，<b>一群加不上请直接加二群</b></p>
            <p>群号：<span class="qq-num">1006740220</span></p>
          </div>
          <div class="qq-actions">
            <a class="qq-join" href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5"/><path d="M5 12l7-7 7 7"/></svg>
              加入群聊
            </a>
            <CopyBtn text="1006740220" label="复制群号" />
          </div>
        </div>
        <div class="qq-block">
          <div class="qq-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>
          </div>
          <div class="qq-info">
            <b>AQUA 开源社区 · QQ 频道</b>
            <p>大版本更新等重要公告同步于此，欢迎加入共建开源社区</p>
            <p>频道号：<span class="qq-num">pd57362562</span></p>
          </div>
          <div class="qq-actions">
            <a class="qq-join" href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5"/><path d="M5 12l7-7 7 7"/></svg>
              加入频道
            </a>
            <CopyBtn text="pd57362562" label="复制频道号" />
          </div>
        </div>
        <div class="qq-block">
          <div class="qq-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26L21.5 9.27l-4.75 4.63 1.12 6.53L12 17.77l-5.87 3.09 1.12-6.53L2.5 9.27l6.6-1.01L12 2z"/></svg>
          </div>
          <div class="qq-info">
            <b>AQUA 完全开源 · Gitee 仓库</b>
            <p>ACU 工程系列旗舰项目 · 网关 + 前台全量源码，基于 <b>AGPL-3.0</b>：可自由自部署，二开对外提供服务需同协议开源</p>
            <p>地址：<span class="qq-num">gitee.com/xiaosu4610/aqua-rust-workers</span></p>
          </div>
          <div class="qq-actions">
            <a class="qq-join" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" title="Gitee · 国内访问推荐">
              <svg viewBox="0 0 24 24" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26L21.5 9.27l-4.75 4.63 1.12 6.53L12 17.77l-5.87 3.09 1.12-6.53L2.5 9.27l6.6-1.01L12 2z"/></svg>
              访问仓库 / Star
            </a>
            <CopyBtn text="https://gitee.com/xiaosu4610/aqua-rust-workers" label="复制仓库地址" />
          </div>
        </div>
        <div class="qq-block">
          <div class="qq-icon">
            <svg viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
          </div>
          <div class="qq-info">
            <b>AQUA 完全开源 · GitHub 仓库</b>
            <p>独立仓库，与 Gitee 内容完全同步 · ACU 工程系列旗舰项目；国际访问推荐，欢迎 Follow 与 Star</p>
            <p>地址：<span class="qq-num">github.com/xiaosu4610/aqua-rust-workers</span></p>
          </div>
          <div class="qq-actions">
            <a class="qq-join" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" title="GitHub · 国际访问">
              <svg viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
              访问仓库 / Star
            </a>
            <CopyBtn text="https://github.com/xiaosu4610/aqua-rust-workers" label="复制仓库地址" />
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 板块四：三步接入 ===== -->
    <section id="h-start" class="block">
      <h2 class="sec-title"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg></span>三步接入</h2>
      <div class="cards">
        <div class="card">
          <h3><i class="step-no">STEP 01</i><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg></span>Base URL（接口地址）</h3>
          <div class="copy-row">
            <code>{{ baseUrl }}</code>
            <CopyBtn :text="baseUrl" />
          </div>
          <p class="hint">客户端只需填入该地址，系统自动拼接 /chat/completions 等路径。</p>
        </div>
        <div class="card">
          <h3><i class="step-no">STEP 02</i><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="7.5" cy="15.5" r="4.5"/><path d="M11 12 21 2"/><path d="M15.5 7.5 18 10"/></svg></span>API Key（密钥）</h3>
          <div class="copy-row">
            <code>注册登录 → 控制台创建密钥</code>
            <router-link class="btn" to="/console" style="text-decoration:none;">进入控制台</router-link>
          </div>
          <p class="hint">本站采用账号制：<b>注册登录后，在个人控制台一键创建 API 密钥</b>（形如 <code>sk-****</code>）——<b>注册免费、创建自助、随时吊销重建</b>。每个密钥独立统计用量。大版本更新与公告在 <b>QQ 频道</b>（频道号 <b>pd57362562</b>）通知，欢迎进频道交流。</p>
        </div>
      </div>
      <p class="hint" style="margin-top:10px;">请求示例、全部端点与错误码：见顶部「<router-link to="/api" style="color:var(--accent);">API 文档</router-link>」页；可用模型与实时健康分见「<router-link to="/models" style="color:var(--accent);">模型中心</router-link>」。</p>
    </section>
  </section>
</template>
