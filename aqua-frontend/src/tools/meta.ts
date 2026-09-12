/* 工具展示层元数据：图标与端点徽标（旧版散落在工具箱卡片/toolShell 的 SVG 与 tag 文案，
 * registry 未收录，这里按工具 id 集中补齐；仅供 ToolsPage 卡片与 ToolPage 工具头使用） */

/** 图标 SVG 内部元素（包裹层：<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">） */
export const TOOL_ICONS: Record<string, string> = {
  tictactoe: '<path d="M4 4h16v16H4z"/><path d="M4 9.33h16M4 14.67h16M9.33 4v16M14.67 4v16"/>',
  gomoku: '<circle cx="7" cy="7" r="3"/><circle cx="17" cy="17" r="3"/><circle cx="17" cy="7" r="3" fill="currentColor"/><circle cx="7" cy="17" r="3" fill="currentColor"/>',
  guess: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/>',
  idiom: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>',
  ip: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3c2.5 2.6 4 5.6 4 9s-1.5 6.4-4 9c-2.5-2.6-4-5.6-4-9s1.5-6.4 4-9z"/>',
  moderation: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M9 12l2 2 4-4"/>',
  translate: '<path d="M5 8l6 6"/><path d="M4 14l6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="M22 22l-5-10-5 10"/><path d="M14 18h6"/>',
  summary: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/>',
  textstats: '<path d="M4 7V5h16v2"/><path d="M12 5v14"/><path d="M9 19h6"/>',
  devkit: '<path d="M16 18l6-6-6-6"/><path d="M8 6l-6 6 6 6"/>',
  weekly: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/><path d="M8 15h8M8 19h5"/>',
  regex: '<path d="M4 7V5h16v2"/><path d="M12 5v14"/><path d="M9 19h6"/><circle cx="17.5" cy="17.5" r="3.5"/><path d="M20 20l2 2"/>',
  polish: '<path d="M12 19l7-7 3 3-7 7-3-3z"/><path d="M18 13l-1.5-7.5L2 2l3.5 14.5L13 18l5-5z"/><path d="M2 2l7.586 7.586"/><circle cx="11" cy="11" r="2"/>',
  hash: '<path d="M4 9h16"/><path d="M4 15h16"/><path d="M10 3 8 21"/><path d="M16 3l-2 18"/>',
  password: '<rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  color: '<circle cx="12" cy="12" r="9"/><circle cx="8.5" cy="10" r="1.2" fill="currentColor"/><circle cx="12" cy="7.5" r="1.2" fill="currentColor"/><circle cx="15.5" cy="10" r="1.2" fill="currentColor"/><path d="M12 21a9 9 0 0 0 0-18"/>',
  'token-count': '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3"/>',
  shorten: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
  webhook: '<path d="M18 16.98h-5.99c-1.1 0-1.95.94-2.48 1.9A4 4 0 0 1 2 17c.01-.7.2-1.4.57-2"/><path d="m6 17 3.13-5.78c.53-.97.1-2.18-.27-3.22A4 4 0 1 1 15.5 5.5"/><path d="m12 6 3.14 5.76c.5.96 1.67 1.11 2.75 1.24A4 4 0 1 1 18 21"/>',
}

/** 端点/驱动方式徽标（旧版 toolShell 第三参与工具箱卡片 tool-tag 文案） */
export const TOOL_TAGS: Record<string, string> = {
  ip: '/v1/ip_location',
  moderation: '/v1/moderations · security-semantic-filtering',
  translate: 'chat 端点驱动',
  summary: 'chat 端点驱动',
  tictactoe: '纯算法 / LLM 对手',
  gomoku: '评分引擎 / LLM 落子',
  guess: '纯算法 / LLM 推理',
  idiom: 'LLM 驱动',
  textstats: '/v1/tools/text-stats',
  devkit: '/v1/tools/*',
  weekly: 'chat 端点驱动',
  regex: 'chat 端点驱动',
  polish: 'chat 端点驱动',
  hash: '/v1/tools/hash',
  password: '/v1/tools/password',
  color: '/v1/tools/color',
  'token-count': '/v1/tools/token-count',
  shorten: '/v1/tools/shorten',
  webhook: '/v1/tools/webhook',
}

/** 工具箱卡片徽标与 toolShell 徽标不一致的工具（旧版卡片文案照搬） */
export const TOOL_CARD_TAGS: Record<string, string> = {
  gomoku: '纯算法 / LLM 辅助',
}
