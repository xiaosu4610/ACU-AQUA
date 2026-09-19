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
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}
function apply(t: Theme) {
  document.documentElement.setAttribute('data-theme', t)
  document.documentElement.style.colorScheme = t
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (meta) meta.content = t === 'dark' ? '#071019' : '#f5f8fa'
}

export function useTheme() {
  function set(t: Theme) { cur.value = t; apply(t); try { localStorage.setItem('aqua-theme', t) } catch { /* 忽略 */ } }
  return { theme: cur, set }
}
