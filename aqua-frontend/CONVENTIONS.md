# AQUA api 前端 4.0 开发约定（移植期必读）

## 项目性质
将旧版单文件前端（5899 行）全面重构为 Vite + Vue 3 + TS 工程。
**参考源（只读，严禁修改）**：`D:\AQUA api-ACU\aqua-worker\frontend\public\index.html`
- 行 1-1269：CSS（已 1:1 提取到 `src/styles/legacy.css`，全局生效，勿重复定义）
- 行 1270-2698：HTML 各页面结构
- 行 2699-5897：JS 逻辑（含 TOOL_REGISTRY、各工具/游戏渲染函数）

## 铁律
1. **视觉 100% 平移**：全部旧 CSS 类可用（legacy.css 已全局引入）。页面根元素用 `<section class="route-page">`（**禁止**使用 `page` 类，那是旧版 display 切换用的，会导致页面不可见）。
2. **文案一字不改**：所有中文文案、链接、QQ 群/频道号照搬旧版。
3. **不改 legacy.css**：需要微调样式时写组件内 `<style scoped>`。
4. TS 宽松：`<script setup lang="ts">`，可省类型标注，不许引入新依赖。

## 已有基建（必须复用，勿重复造）
```ts
// @/composables/useApi
GATEWAY                       // 网关基址（如 https://aqua.zhuafs.com/v1），拼接路径用 GATEWAY + '/xxx'
apiJson<T>(path, { method?, body?, key?, signal? })  // 自动 JSON 头/Bearer/错误中文化；失败 throw {status, code, message}
errText(e)                    // 取中文错误消息
copyText(text): Promise<boolean>
fmt(n)                        // 千分位

// @/composables/useSSE
streamChat({ model, messages, key?, signal?, temperature?, onDelta })  // 返回完整文本
stripThink(s)                 // 树洞用：剥 </think> 思维链

// @/composables/useModels —— 全局单例（多页面共享，去重 + auto 置顶 + 离线兜底）
const { models, loading, error, loadedAt, load } = useModels()
// models: ModelRow[] { id, platform, type, status?, status_msg? }；实时性能（FRT/TPS）走 /v1/models/status
// 切页时调 load() 即可（内部防重复请求）

// @/composables/modelMeta
classifyModel(id) / platformLabel(p) / typeLabel(t) / hideTag(type)
fallbackModels / dsRetired() / dsMaintenance(model)   // DeepSeek 下线守卫

// @/components/CopyBtn.vue
<CopyBtn :text="xxx" label="复制" />   // 自带"已复制"反馈；pre 内用 class="pre-copy"（绝对定位右上角）
```

## 关键常量
- 演示密钥：`'sk-playground-demo'`（开放模式任意非空密钥均可用）
- 常用端点：`/models` `/chat/completions` `/usage` `/status` `/arena/*`（竞技场具体端点见旧版 JS）
- 旧版全局函数 `copyText(text, btn)` 的"已复制"反馈 → 用 CopyBtn 组件等价替代

## 页面骨架模板
```vue
<script setup lang="ts">
import { onMounted } from 'vue'
// ... 业务 import
onMounted(() => { /* 等价旧版 xxxInit() */ })
</script>

<template>
  <section class="route-page">
    <!-- 旧版对应 <section id="page-xxx" class="page"> 内的 HTML，1:1 平移；
         id="page-xxx" 不要保留（避免与旧版 CSS 的 display:none 冲突），内部结构 id/class 保留 -->
  </section>
</template>
```

## 事件绑定改写对照
- 旧 `data-copy-text="xxx"` 按钮 → `<CopyBtn text="xxx" />`
- 旧 `data-copy="元素id"` → CopyBtn 传等价文本（或读 ref）
- 旧 `onclick="fn()"` → `@click="fn"`
- 旧 `innerHTML = '...'` 动态渲染 → v-for / v-html（仅内部数据源允许 v-html，用户输入一律插值转义）

## 验收（自检清单）
- [ ] vue-tsc 无新增错误（可跑 `npx vue-tsc --noEmit` 自查，只关注自己新增的文件）
- [ ] 无横向溢出风险：长 ID/代码段放 `pre` 或 `code`（legacy.css 已有溢出处理）
- [ ] 旧版该页的每个交互（点击/切换/复制/加载态/错误态/离线兜底）都有对应实现
