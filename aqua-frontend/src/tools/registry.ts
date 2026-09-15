/* 工具箱注册表：一个工具 = 一个目录（src/tools/<id>/Tool.vue）+ 这里一条注册
 * 新增工具只需：建目录 → 写 Tool.vue → 在 REGISTRY 里加一条（自动出现在工具箱页面）
 * 条目顺序与旧版 TOOL_REGISTRY 一致（工具箱页面按分类分组展示，组内保持此顺序） */
export interface ToolMeta {
  id: string
  title: string
  desc: string
  /** 分类：text 文本处理 / dev 开发者 / net 网络 / ai AI 驱动 / game 游戏 */
  category: 'text' | 'dev' | 'net' | 'ai' | 'game'
  /** 纯前端工具不消耗上游额度 */
  local?: boolean
  load: () => Promise<any>
}

export const TOOL_REGISTRY: Record<string, ToolMeta> = {
  ip: {
    id: 'ip',
    title: 'IP 归属地查询',
    desc: '输入 IP 查询地理位置与运营商，支持 AI 解读网络环境',
    category: 'net',
    load: () => import('@/tools/ip/Tool.vue'),
  },
  moderation: {
    id: 'moderation',
    title: '文本内容审核',
    desc: '检测文本风险类别，AI 解释风险点并给出改写建议',
    category: 'ai',
    load: () => import('@/tools/moderation/Tool.vue'),
  },
  translate: {
    id: 'translate',
    title: 'AI 翻译',
    desc: '多语种互译，自动识别源语言，译文自然流畅',
    category: 'ai',
    load: () => import('@/tools/translate/Tool.vue'),
  },
  summary: {
    id: 'summary',
    title: 'AI 摘要',
    desc: '长文一键提炼要点、一段话总结或大纲结构',
    category: 'ai',
    load: () => import('@/tools/summary/Tool.vue'),
  },
  tictactoe: {
    id: 'tictactoe',
    title: '井字棋对弈',
    desc: '挑战必不败的 minimax 算法，或切换 LLM 模型对手，体验算法与 AI 的思维差异',
    category: 'game',
    load: () => import('@/tools/tictactoe/Tool.vue'),
  },
  gomoku: {
    id: 'gomoku',
    title: '五子棋对弈',
    desc: '9×9 棋盘挑战评分算法引擎，也可开启 LLM 落子模式看 AI 怎么想',
    category: 'game',
    load: () => import('@/tools/gomoku/Tool.vue'),
  },
  guess: {
    id: 'guess',
    title: '猜数字 1A2B',
    desc: '经典推理游戏：你猜 AI 的数字，或开启挑战模式让 LLM 猜你的数字',
    category: 'game',
    load: () => import('@/tools/guess/Tool.vue'),
  },
  idiom: {
    id: 'idiom',
    title: '成语接龙',
    desc: '与 LLM 轮流接龙，首尾字自动校验，词穷即输',
    category: 'game',
    load: () => import('@/tools/idiom/Tool.vue'),
  },
  textstats: {
    id: 'textstats',
    title: '文本统计',
    desc: '字数、词频 Top10、阅读时长即时统计，纯算法零等待',
    category: 'text',
    local: true,
    load: () => import('@/tools/textstats/Tool.vue'),
  },
  devkit: {
    id: 'devkit',
    title: '开发者工具箱',
    desc: 'UUID 生成、时间戳互转、Base64/URL 编解码、JSON 格式化',
    category: 'dev',
    local: true,
    load: () => import('@/tools/devkit/Tool.vue'),
  },
  weekly: {
    id: 'weekly',
    title: '周报生成器',
    desc: '本周流水账一键炼成结构化周报：核心产出、进行中、风险与下周计划',
    category: 'ai',
    load: () => import('@/tools/weekly/Tool.vue'),
  },
  regex: {
    id: 'regex',
    title: '正则解释器',
    desc: '描述生成正则、正则翻译成人话，双向转换并标注语言方言差异',
    category: 'ai',
    load: () => import('@/tools/regex/Tool.vue'),
  },
  polish: {
    id: 'polish',
    title: '文本润色',
    desc: '润色、极简、扩写三种模式，不改本意只改表达，拒绝空话套话',
    category: 'ai',
    load: () => import('@/tools/polish/Tool.vue'),
  },
  hash: {
    id: 'hash',
    title: '哈希计算',
    desc: 'MD5 / SHA1 / SHA256 / SHA512 一键计算，校验文件指纹必备',
    category: 'dev',
    load: () => import('@/tools/hash/Tool.vue'),
  },
  password: {
    id: 'password',
    title: '密码生成器',
    desc: '自定义长度与字符类型批量生成强密码，服务端随机熵',
    category: 'dev',
    load: () => import('@/tools/password/Tool.vue'),
  },
  color: {
    id: 'color',
    title: '颜色转换',
    desc: 'HEX / RGB / HSL 三格式互转，取色适配设计场景',
    category: 'dev',
    local: true,
    load: () => import('@/tools/color/Tool.vue'),
  },
  'token-count': {
    id: 'token-count',
    title: 'Token 估算',
    desc: '估算文本 token 消耗（中英文分别计价），控制上下文长度',
    category: 'dev',
    load: () => import('@/tools/token-count/Tool.vue'),
  },
  shorten: {
    id: 'shorten',
    title: '短链生成',
    desc: '长网址一键变短链（aqua.zhuafs.com/s/xxx），90 天无访问自动清理',
    category: 'net',
    load: () => import('@/tools/shorten/Tool.vue'),
  },
  webhook: {
    id: 'webhook',
    title: 'Webhook 调试',
    desc: '一键生成收集地址，抓取任意请求的 Header/Body，调试回调利器',
    category: 'net',
    load: () => import('@/tools/webhook/Tool.vue'),
  },
}

export const TOOL_CATEGORY_LABEL: Record<ToolMeta['category'], string> = {
  text: '文本处理',
  dev: '开发者',
  net: '网络查询',
  ai: 'AI 驱动',
  game: '游戏',
}

export function toolCount(): number {
  return Object.keys(TOOL_REGISTRY).length
}
