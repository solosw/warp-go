import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { GetAppearance, SaveAppearance, SelectBackgroundImage, GetBackgroundImageData } from '../../wailsjs/go/main/App'

/** Panel opacity floor, mirroring config.Appearance.Normalize in Go. */
const MIN_PANEL_OPACITY = 0.15

function clamp(v: number, min: number, max: number): number {
  if (Number.isNaN(v)) return min
  return Math.min(Math.max(v, min), max)
}

export const useAppearanceStore = defineStore('appearance', () => {
  const backgroundImage = ref('')
  const backgroundOpacity = ref(0.35)
  const panelOpacity = ref(0.85)
  /** Data URL for the current image. Empty when no image is set. */
  const imageData = ref('')
  const error = ref('')

  const hasBackground = computed(() => !!backgroundImage.value && !!imageData.value)

  /**
   * Inline style for the background layer. The image is applied here rather than
   * through a CSS custom property: large base64 data URLs are unreliable as
   * custom-property values in WebView2, and inline style is the robust path.
   */
  const backgroundLayerStyle = computed(() => {
    if (!hasBackground.value) {
      return {
        backgroundImage: 'none',
        opacity: '0',
      }
    }
    // No surrounding quotes: extra quotes break some WebView CSS parsers on long base64.
    return {
      backgroundImage: `url(${imageData.value})`,
      opacity: String(backgroundOpacity.value),
    }
  })

  /**
   * Push panel transparency into :root. Every panel reads --panel-alpha via
   * the --surface-* colours defined in style.css.
   */
  function apply() {
    document.documentElement.style.setProperty('--panel-alpha', String(panelOpacity.value))
  }

  async function loadImageData(path: string) {
    if (!path) {
      imageData.value = ''
      return
    }
    try {
      imageData.value = (await GetBackgroundImageData(path)) || ''
      error.value = imageData.value ? '' : '无法加载背景图片'
    } catch (e: any) {
      imageData.value = ''
      error.value = e?.message || String(e)
    }
  }

  async function load() {
    try {
      const ap = await GetAppearance()
      if (ap) {
        backgroundImage.value = ap.backgroundImage || ''
        backgroundOpacity.value = clamp(ap.backgroundOpacity, 0, 1)
        panelOpacity.value = clamp(ap.panelOpacity, MIN_PANEL_OPACITY, 1)
      }
    } catch (e: any) {
      error.value = e?.message || String(e)
    }
    // New settings contain a data URL. Keep loading old path-based settings
    // so existing users get a transparent migration on the next save.
    if (backgroundImage.value.startsWith('data:image/')) {
      imageData.value = backgroundImage.value
    } else {
      const oldPath = backgroundImage.value
      await loadImageData(oldPath)
      if (imageData.value) {
        // Migrate legacy path-based settings after the image is loaded.
        backgroundImage.value = imageData.value
        await persist()
      }
    }
    apply()
  }

  async function persist() {
    try {
      await SaveAppearance({
        backgroundImage: backgroundImage.value,
        backgroundOpacity: backgroundOpacity.value,
        panelOpacity: panelOpacity.value,
      } as any)
      error.value = ''
    } catch (e: any) {
      error.value = e?.message || String(e)
    }
  }

  /** Update opacity values live; caller persists when the user is done. */
  function setBackgroundOpacity(v: number) {
    backgroundOpacity.value = clamp(v, 0, 1)
  }

  function setPanelOpacity(v: number) {
    panelOpacity.value = clamp(v, MIN_PANEL_OPACITY, 1)
    apply()
  }

  async function pickImage() {
    try {
      const picked = await SelectBackgroundImage()
      if (!picked) return
      const data = await GetBackgroundImageData(picked)
      if (!data) return
      // Store the image itself instead of its local path so it survives moves
      // and remains available after restarting the application.
      backgroundImage.value = data
      imageData.value = data
      apply()
      await persist()
    } catch (e: any) {
      error.value = e?.message || String(e)
    }
  }

  async function clearImage() {
    backgroundImage.value = ''
    imageData.value = ''
    apply()
    await persist()
  }

  async function reset() {
    backgroundImage.value = ''
    imageData.value = ''
    backgroundOpacity.value = 0.35
    panelOpacity.value = 0.85
    apply()
    await persist()
  }

  return {
    backgroundImage, backgroundOpacity, panelOpacity, imageData, error,
    hasBackground, backgroundLayerStyle,
    load, apply, persist, pickImage, clearImage, reset,
    setBackgroundOpacity, setPanelOpacity, MIN_PANEL_OPACITY,
  }
})
