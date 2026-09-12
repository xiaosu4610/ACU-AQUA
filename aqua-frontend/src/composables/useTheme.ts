/* 主题：light / dark（深蓝），与旧版 localStorage 键名兼容（aqua-theme） */
import { ref } from 'vue'

export type Theme = 'light' | 'dark'

const cur = ref<Theme>(read())
apply(cur.value)

function read(): Theme {
  try {
    const t = localStorage.getItem('aqua-theme')
    if (t === 'light' || t === 'dark') return t
  } catch { /* 隐私模式 */ }
  return 'light'
}
function apply(t: Theme) {
  document.documentElement.setAttribute('data-theme', t)
}

export function useTheme() {
  function set(t: Theme) { cur.value = t; apply(t); try { localStorage.setItem('aqua-theme', t) } catch { /* 忽略 */ } }
  return { theme: cur, set }
}
