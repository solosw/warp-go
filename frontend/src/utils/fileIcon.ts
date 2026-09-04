import iconSet from '@iconify-json/vscode-icons/icons.json'

type IconData = { body: string; width?: number; height?: number; left?: number; top?: number }
type IconSet = { icons: Record<string, IconData>; width?: number; height?: number }

const iconData = iconSet as IconSet
const icons = iconData.icons
const DEFAULT_WIDTH = iconData.width || iconData.height || 16
const DEFAULT_HEIGHT = iconData.height || iconData.width || 16
const FALLBACK_FILE = 'default-file'
const FALLBACK_FOLDER = 'default-folder'
const FALLBACK_FOLDER_OPEN = 'default-folder-opened'

const fileIconByName: Record<string, string> = {
  '.gitignore': 'file-type-git', '.gitattributes': 'file-type-git', '.gitmodules': 'file-type-git',
  '.env': 'file-type-dotenv', '.env.local': 'file-type-dotenv', '.env.development': 'file-type-dotenv', '.env.production': 'file-type-dotenv',
  'dockerfile': 'file-type-docker', 'docker-compose.yml': 'file-type-docker', 'docker-compose.yaml': 'file-type-docker',
  'makefile': 'file-type-makefile', 'cmakelists.txt': 'file-type-cmake',
  'package.json': 'file-type-npm', 'package-lock.json': 'file-type-npm', 'yarn.lock': 'file-type-yarn', 'pnpm-lock.yaml': 'file-type-pnpm',
  'tsconfig.json': 'file-type-tsconfig', 'vite.config.ts': 'file-type-vite', 'vite.config.js': 'file-type-vite',
  'readme.md': 'file-type-readme', 'license': 'file-type-license', 'license.md': 'file-type-license',
}

const fileIconByExtension: Record<string, string> = {
  ts: 'file-type-typescript', tsx: 'file-type-reactts', mts: 'file-type-typescript', cts: 'file-type-typescript',
  js: 'file-type-js', jsx: 'file-type-reactjs', mjs: 'file-type-js', cjs: 'file-type-js',
  vue: 'file-type-vue', svelte: 'file-type-svelte', astro: 'file-type-astro',
  html: 'file-type-html', htm: 'file-type-html', css: 'file-type-css', scss: 'file-type-scss', sass: 'file-type-sass', less: 'file-type-less',
  json: 'file-type-json', jsonc: 'file-type-json', json5: 'file-type-json', xml: 'file-type-xml', svg: 'file-type-svg',
  yaml: 'file-type-yaml', yml: 'file-type-yaml', toml: 'file-type-toml', ini: 'file-type-ini',
  md: 'file-type-markdown', mdx: 'file-type-markdown', markdown: 'file-type-markdown',
  go: 'file-type-go', py: 'file-type-python', pyw: 'file-type-python', ipynb: 'file-type-jupyter',
  rs: 'file-type-rust', c: 'file-type-c', h: 'file-type-c', cpp: 'file-type-cpp', cxx: 'file-type-cpp', cc: 'file-type-cpp', hpp: 'file-type-cpp',
  java: 'file-type-java', kt: 'file-type-kotlin', kts: 'file-type-kotlin', scala: 'file-type-scala', groovy: 'file-type-groovy',
  php: 'file-type-php', cs: 'file-type-csharp', rb: 'file-type-ruby', swift: 'file-type-swift',
  sh: 'file-type-shell', bash: 'file-type-shell', zsh: 'file-type-shell', fish: 'file-type-shell', ps1: 'file-type-powershell', bat: 'file-type-bat', cmd: 'file-type-bat',
  sql: 'file-type-sql', graphql: 'file-type-graphql', gql: 'file-type-graphql',
  lua: 'file-type-lua', r: 'file-type-r', dart: 'file-type-dart', ex: 'file-type-elixir', exs: 'file-type-elixir',
  hs: 'file-type-haskell', erl: 'file-type-erlang', clj: 'file-type-clojure', cljs: 'file-type-clojure',
  png: 'file-type-image', jpg: 'file-type-image', jpeg: 'file-type-image', gif: 'file-type-image', webp: 'file-type-image', ico: 'file-type-image', bmp: 'file-type-image',
  pdf: 'file-type-pdf',
  xlsx: 'file-type-excel', xls: 'file-type-excel', csv: 'file-type-excel',
  docx: 'file-type-word', doc: 'file-type-word',
  zip: 'file-type-zip', gz: 'file-type-zip', tar: 'file-type-zip',
  ttf: 'file-type-font', otf: 'file-type-font', woff: 'file-type-font', woff2: 'file-type-font',
}

const folderIconByName: Record<string, string> = {
  '.git': 'folder-git', '.github': 'folder-github', '.vscode': 'folder-vscode', 'node_modules': 'folder-node',
  'src': 'folder-src', 'dist': 'folder-dist', 'build': 'folder-dist', 'public': 'folder-public', 'assets': 'folder-assets',
  'components': 'folder-components', 'config': 'folder-config', 'tests': 'folder-test', 'test': 'folder-test',
  'docs': 'folder-docs', 'scripts': 'folder-scripts', 'styles': 'folder-css', 'images': 'folder-images',
}

function resolveIcon(name: string, fallback: string): IconData {
  return icons[name] || icons[fallback]
}

function iconToDataURL(name: string, fallback: string): string {
  const icon = resolveIcon(name, fallback)
  const width = icon.width || DEFAULT_WIDTH
  const height = icon.height || DEFAULT_HEIGHT
  const left = icon.left || 0
  const top = icon.top || 0
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${left} ${top} ${width} ${height}" width="${width}" height="${height}">${icon.body}</svg>`
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
}

function fileBaseName(filePath: string): string {
  return filePath.replace(/\\/g, '/').split('/').pop()?.toLowerCase() || ''
}

export function getFileIcon(filePath: string): string {
  const base = fileBaseName(filePath)
  const extension = base.split('.').pop() || ''
  return iconToDataURL(fileIconByName[base] || fileIconByExtension[extension] || FALLBACK_FILE, FALLBACK_FILE)
}

export function getFolderIcon(folderName: string, open = false): string {
  const base = fileBaseName(folderName)
  const icon = folderIconByName[base] || (open ? FALLBACK_FOLDER_OPEN : FALLBACK_FOLDER)
  const openedIcon = open && icons[`${icon}-opened`] ? `${icon}-opened` : icon
  return iconToDataURL(openedIcon, open ? FALLBACK_FOLDER_OPEN : FALLBACK_FOLDER)
}
