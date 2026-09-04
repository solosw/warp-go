export type PreviewKind = 'text' | 'image' | 'pdf' | 'spreadsheet' | 'word'

const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'svg'])
const PDF_EXT = new Set(['pdf'])
const SHEET_EXT = new Set(['xlsx', 'xls', 'csv'])
const WORD_EXT = new Set(['docx', 'doc'])

export function fileExt(path: string): string {
  const base = path.replace(/\\/g, '/').split('/').pop() || ''
  const i = base.lastIndexOf('.')
  return i >= 0 ? base.slice(i + 1).toLowerCase() : ''
}

export function previewKind(path: string): PreviewKind {
  const ext = fileExt(path)
  if (IMAGE_EXT.has(ext)) return 'image'
  if (PDF_EXT.has(ext)) return 'pdf'
  if (SHEET_EXT.has(ext)) return 'spreadsheet'
  if (WORD_EXT.has(ext)) return 'word'
  return 'text'
}

export function isPreviewableBinary(path: string): boolean {
  return previewKind(path) !== 'text'
}
