// NimVor SQL editor: CodeMirror 6 with PostgreSQL highlighting, schema-aware completion, search, history and a
// formatter, exposed as window.NimSqlEditor (the page script drives it; nothing here talks to the server).
import { EditorState, Compartment } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, placeholder } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { sql, PostgreSQL } from '@codemirror/lang-sql';
import { autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete';
import { syntaxHighlighting, HighlightStyle, bracketMatching, indentOnInput } from '@codemirror/language';
import { tags as t } from '@lezer/highlight';
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search';
import { format } from 'sql-formatter';

// Colours come from the panel's own theme variables, so they stay readable in light and dark mode (CodeMirror's default
// palette is made for a white background). Same mapping the panel's other SQL highlighting uses.
var nimHighlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: 'var(--bs-primary-text-emphasis)', fontWeight: '600' },
  { tag: [t.string, t.special(t.string)], color: 'var(--bs-success-text-emphasis)' },
  { tag: [t.number, t.bool, t.null], color: 'var(--bs-warning-text-emphasis)' },
  { tag: [t.typeName, t.standard(t.typeName), t.className], color: 'var(--bs-info-text-emphasis)' },
  { tag: [t.lineComment, t.blockComment, t.comment], color: 'var(--bs-secondary-color)', fontStyle: 'italic' },
  { tag: [t.operator, t.punctuation, t.paren, t.separator], color: 'var(--bs-secondary-color)' },
  { tag: t.invalid, color: 'var(--bs-danger-text-emphasis)' }
]);

// The panel's Content-Security-Policy has style-src 'self': CodeMirror's usual <style> element in the page head is
// blocked, which left the editor unstyled. Inside a shadow root CodeMirror applies its styles as constructed
// stylesheets (adoptedStyleSheets), which the policy does not restrict, so the strict policy stays as it is. The panel's
// CSS variables (--bs-*) still inherit into the shadow tree, so the editor follows light and dark mode.
function mountPoint(host) {
  if (!host.attachShadow || !window.CSSStyleSheet || !('adoptedStyleSheets' in document)) { return host; }
  var root = host.shadowRoot || host.attachShadow({ mode: 'open' });
  var sheet = new CSSStyleSheet();
  sheet.replaceSync(':host{display:block}.nim-cm-mount{height:100%}');
  root.adoptedStyleSheets = [sheet];
  var mount = document.createElement('div');
  mount.className = 'nim-cm-mount';
  root.appendChild(mount);
  return mount;
}

function create(parent, opts) {
  opts = opts || {};
  parent = mountPoint(parent);
  var lang = new Compartment();
  var languageFor = function (schema) { return sql({ dialect: PostgreSQL, schema: schema || {}, upperCaseKeywords: false }); };
  var run = opts.onRun || function () {};
  var view = new EditorView({
    parent: parent,
    state: EditorState.create({
      doc: opts.doc || '',
      extensions: [
        lineNumbers(), highlightActiveLineGutter(), history(), drawSelection(), indentOnInput(), bracketMatching(), closeBrackets(),
        highlightActiveLine(), highlightSelectionMatches(), syntaxHighlighting(nimHighlight),
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
