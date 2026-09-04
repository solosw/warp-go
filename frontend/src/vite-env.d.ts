/// <reference types="vite/client" />

declare module '*.wasm?url' {
  const url: string
  export default url
}

declare module 'monaco-volar' {
  import type * as monaco from 'monaco-editor'

  export function loadTheme(editor: typeof monaco.editor): Promise<{ dark: string; light: string }>
  export function loadGrammars(
    monacoApi: typeof monaco,
    editor: monaco.editor.IStandaloneCodeEditor,
  ): Promise<void>
}

declare module 'monaco-editor-copilot' {
  import type * as monaco from 'monaco-editor'

  interface CopilotConfig {
    openaiKey?: string
    openaiUrl?: string
    openaiParams?: {
      model?: string
      temperature?: number
      max_tokens?: number
      top_p?: number
      frequency_penalty?: number
      presence_penalty?: number
      stop?: string[]
    }
    maxCodeLinesToOpenai?: number
    assistantMessage?: string
  }

  const MonacoEditorCopilot: (
    editor: monaco.editor.IStandaloneCodeEditor,
    config: CopilotConfig,
  ) => () => void

  export default MonacoEditorCopilot
}

declare module '*.vue' {
    import type {DefineComponent} from 'vue'
    const component: DefineComponent<{}, {}, any>
    export default component
}

declare module 'xlsx' {
  export function read(data: ArrayBuffer | Uint8Array, opts?: any): any
  export const utils: {
    sheet_to_json(sheet: any, opts?: any): any
  }
}

declare module 'mammoth/mammoth.browser' {
  export function convertToHtml(input: { arrayBuffer: ArrayBuffer }): Promise<{ value: string }>
  const mammoth: { convertToHtml: typeof convertToHtml }
  export default mammoth
}
