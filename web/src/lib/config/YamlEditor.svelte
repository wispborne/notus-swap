<script lang="ts">
  import { yaml } from '@codemirror/lang-yaml'
  import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
  import { lintGutter, setDiagnostics, type Diagnostic } from '@codemirror/lint'
  import { tags as t } from '@lezer/highlight'
  import { basicSetup, EditorView } from 'codemirror'
  import { EditorState } from '@codemirror/state'
  import { onMount } from 'svelte'

  // A YAML editor in the Sigma colours. The parent sets the text with
  // setText() and hears every edit through onChange. `problem` marks one line
  // (from the server's check) as an error.
  let {
    onChange,
    problem = null,
  }: { onChange: (text: string) => void; problem?: { line: number; message: string } | null } = $props()

  let box: HTMLDivElement
  let view: EditorView | undefined
  let silent = false // true while setText replaces the text

  const theme = EditorView.theme(
    {
      '&': { height: '100%', backgroundColor: 'var(--color-sunken)', color: 'var(--color-text)', fontSize: '12.5px' },
      '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.5' },
      '.cm-content': { caretColor: 'var(--color-primary)' },
      '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--color-primary)' },
      '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'color-mix(in srgb, var(--color-primary) 20%, transparent) !important' },
      '.cm-gutters': { backgroundColor: 'var(--color-panel)', color: 'var(--color-dim)', border: 'none', borderRight: '1px solid var(--color-line)' },
      '.cm-activeLineGutter': { backgroundColor: 'var(--color-hover)', color: 'var(--color-text)' },
      '.cm-activeLine': { backgroundColor: '#ffffff08' },
      '.cm-foldPlaceholder': { backgroundColor: 'var(--color-panel2)', border: '1px solid var(--color-line)', color: 'var(--color-muted)' },
      '.cm-searchMatch': { backgroundColor: '#f0b85a44' },
      '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: '#f0b85a88' },
      '.cm-panels': { backgroundColor: 'var(--color-panel)', color: 'var(--color-text)' },
      '.cm-panels input, .cm-panels button': { color: 'var(--color-text)' },
      '.cm-tooltip': { backgroundColor: 'var(--color-panel2)', border: '1px solid var(--color-line)', color: 'var(--color-text)' },
      '.cm-diagnostic-error': { borderLeftColor: 'var(--color-err)' },
      '.cm-matchingBracket': { backgroundColor: 'color-mix(in srgb, var(--color-secondary) 13%, transparent)', outline: 'none' },
    },
    { dark: true },
  )
  const colors = HighlightStyle.define([
    { tag: [t.propertyName, t.definition(t.propertyName)], color: 'var(--color-primary)' },
    { tag: [t.string, t.special(t.string)], color: '#f0b85a' },
    { tag: [t.number, t.integer, t.float], color: '#b69cff' },
    { tag: [t.bool, t.null, t.atom, t.keyword], color: '#ff7a9a' },
    { tag: [t.comment, t.lineComment], color: 'var(--color-dim)', fontStyle: 'italic' },
    { tag: [t.meta, t.labelName, t.typeName], color: '#7aa7ff' },
    { tag: [t.punctuation, t.separator, t.bracket], color: 'var(--color-muted)' },
  ])

  onMount(() => {
    view = new EditorView({
      parent: box,
      state: EditorState.create({
        doc: '',
        extensions: [
          basicSetup,
          yaml(),
          theme,
          syntaxHighlighting(colors),
          lintGutter(),
          EditorView.updateListener.of((u) => {
            if (u.docChanged && !silent) onChange(u.state.doc.toString())
          }),
        ],
      }),
    })
    return () => view?.destroy()
  })

  /** Replace the whole text. Does not call onChange. */
  export function setText(text: string) {
    if (!view) return
    silent = true
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } })
    silent = false
  }

  $effect(() => {
    const p = problem
    if (!view) return
    const diags: Diagnostic[] = []
    if (p && p.line >= 1 && p.line <= view.state.doc.lines) {
      const line = view.state.doc.line(p.line)
      diags.push({ from: line.from, to: line.to, severity: 'error', message: p.message })
    }
    view.dispatch(setDiagnostics(view.state, diags))
  })
</script>

<div bind:this={box} class="h-full min-h-0 overflow-hidden rounded-md border border-line"></div>
