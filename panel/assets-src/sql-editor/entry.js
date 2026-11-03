// NimVor SQL editor: CodeMirror 6 with PostgreSQL highlighting, schema-aware completion, search, history and a
// formatter, exposed as window.NimSqlEditor (the page script drives it; nothing here talks to the server).
import { EditorState, Compartment } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, placeholder } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { sql, PostgreSQL } from '@codemirror/lang-sql';
import { autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete';
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching, indentOnInput } from '@codemirror/language';
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search';
import { format } from 'sql-formatter';

function create(parent, opts) {
  opts = opts || {};
  var lang = new Compartment();
  var languageFor = function (schema) { return sql({ dialect: PostgreSQL, schema: schema || {}, upperCaseKeywords: false }); };
  var run = opts.onRun || function () {};
  var view = new EditorView({
    parent: parent,
    state: EditorState.create({
      doc: opts.doc || '',
      extensions: [
        lineNumbers(), highlightActiveLineGutter(), history(), drawSelection(), indentOnInput(), bracketMatching(), closeBrackets(),
        highlightActiveLine(), highlightSelectionMatches(), syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        autocompletion(), lang.of(languageFor(opts.schema)), placeholder(opts.placeholder || ''),
        keymap.of([
          { key: 'Mod-Enter', run: function () { run(); return true; } },
          indentWithTab, ...closeBracketsKeymap, ...completionKeymap, ...searchKeymap, ...historyKeymap, ...defaultKeymap
        ]),
        EditorView.contentAttributes.of({ 'aria-label': 'SQL query', spellcheck: 'false' }),
        EditorView.theme({
          '&': { height: '100%', fontSize: '.85rem', backgroundColor: 'var(--bs-body-bg)', color: 'var(--bs-body-color)' },
          '.cm-scroller': { fontFamily: 'var(--bs-font-monospace)', lineHeight: '1.5' },
          '.cm-gutters': { backgroundColor: 'var(--bs-tertiary-bg)', color: 'var(--bs-secondary-color)', border: 'none' },
          '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--bs-tertiary-bg)' },
          '&.cm-focused': { outline: 'none' }
        })
      ]
    })
  });
  return {
    view: view,
    getText: function () { return view.state.doc.toString(); },
    // The selected text, or the whole editor when nothing is selected.
    getQuery: function () {
      var sel = view.state.selection.main;
      return sel.empty ? view.state.doc.toString() : view.state.sliceDoc(sel.from, sel.to);
    },
    setText: function (t) { view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: t } }); },
    setSchema: function (schema) { view.dispatch({ effects: lang.reconfigure(languageFor(schema)) }); },
    format: function () {
      var t = view.state.doc.toString();
      try { this.setText(format(t, { language: 'postgresql', keywordCase: 'upper', tabWidth: 2 })); return true; } catch (e) { return false; }
    },
    focus: function () { view.focus(); }
  };
}

window.NimSqlEditor = { create: create };
