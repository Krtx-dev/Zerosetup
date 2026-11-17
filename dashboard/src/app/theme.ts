const KEY = 'zerosetup.theme'
type Theme = 'dark' | 'light'

export function theme(): Theme {
  try {
    const t = localStorage.getItem(KEY)
    if (t === 'light' || t === 'dark') return t
  } catch {
    /* storage unavailable */
  }
  return 'dark'
}

export function setTheme(t: Theme) {
  document.documentElement.dataset.theme = t
  try {
    localStorage.setItem(KEY, t)
  } catch {
    /* ignore */
  }
}

export function applyTheme() {
  document.documentElement.dataset.theme = theme()
}
