// 主题切换（§0.2 / REF-tokens）：亮/暗唯一开关 = <body> 上的 data-ds-dark-theme 属性；
// <html> 写 color-scheme；正文字号轴 --dsh-content-font-size（12–17，默认 14）。
import { ref } from 'vue'

export type Theme = 'light' | 'dark'
export type Scheme = Theme | 'system'

const darkMedia = typeof window !== 'undefined'
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null

const savedKey = 'lidsh-theme'

export function useTheme() {
  const scheme = ref<Scheme>(loadScheme())
  const fontSize = ref<number>(loadFontSize())

  function apply() {
    const isDark = scheme.value === 'dark' || (scheme.value === 'system' && !!darkMedia?.matches)
    document.body.toggleAttribute('data-ds-dark-theme', isDark)
    document.documentElement.style.colorScheme = isDark ? 'dark' : 'light'
    document.body.style.setProperty('--dsh-content-font-size', `${fontSize.value}px`)
  }

  function setScheme(s: Scheme) {
    scheme.value = s
    localStorage.setItem(savedKey, s)
    apply()
  }

  function setFontSize(px: number) {
    fontSize.value = Math.max(12, Math.min(17, px))
    localStorage.setItem('lidsh-font-size', String(fontSize.value))
    apply()
  }

  // 系统偏好变化即时跟随（仅 system 模式）。
  darkMedia?.addEventListener('change', () => { if (scheme.value === 'system') apply() })

  return { scheme, fontSize, setScheme, setFontSize, apply }
}

function loadScheme(): Scheme {
  const v = localStorage.getItem(savedKey)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

function loadFontSize(): number {
  const n = Number(localStorage.getItem('lidsh-font-size'))
  return Number.isFinite(n) && n >= 12 && n <= 17 ? n : 14
}
