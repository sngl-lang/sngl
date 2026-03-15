import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from "https://esm.sh/@codemirror/view@6";
import { EditorState } from "https://esm.sh/@codemirror/state@6";
import { StreamLanguage } from "https://esm.sh/@codemirror/language@6";
import { defaultKeymap, history, historyKeymap } from "https://esm.sh/@codemirror/commands@6";
import { oneDark } from "https://esm.sh/@codemirror/theme-one-dark@6";

// SNGL language mode for CodeMirror
const snglMode = {
    startState() {
        return { inBlockComment: false };
    },
    token(stream, state) {
        if (state.inBlockComment) {
            if (stream.skipTo("*/")) {
                stream.next(); stream.next();
                state.inBlockComment = false;
            } else {
                stream.skipToEnd();
            }
            return "comment";
        }
        if (stream.match("/*")) {
            state.inBlockComment = true;
            return "comment";
        }
        if (stream.match("//")) {
            stream.skipToEnd();
            return "comment";
        }
        if (stream.match('"')) {
            while (!stream.eol()) {
                if (stream.next() === '"') break;
            }
            return "string";
        }
        if (stream.match("'")) {
            while (!stream.eol()) {
                if (stream.next() === "'") break;
            }
            return "string";
        }
        if (stream.match(/#[0-9a-fA-F]{3,8}\b/)) return "color";
        if (stream.match(/#(true|false|null)\b/)) return "atom";
        if (stream.match(/[0-9]+(\.[0-9]+)?/)) return "number";
        if (stream.match(/[a-zA-Z_@][a-zA-Z0-9_.-]*/)) {
            const w = stream.current();
            if (/^(component|param|var|computed|if|else|for|import|struct|enum|output|data|app|styles)$/.test(w)) return "keyword";
            if (/^(vbox|hbox|stack|text|button|input|checkbox|image|scroll|spacer)$/.test(w)) return "builtin";
            if (/^(string|int|float|bool|list|map)$/.test(w)) return "type";
            if (w.startsWith("@")) return "meta";
            return "variable";
        }
        if (stream.match(/[{}()\[\]]/)) return "bracket";
        if (stream.match(/[=<>!&|+\-*/]+/)) return "operator";
        stream.next();
        return null;
    },
};

let editor;
let wasmReady = false;
let debounceTimer;
let activeTab = "preview";

const statusEl = document.getElementById("status");
const errorEl = document.getElementById("error");
const previewEl = document.getElementById("preview");
const astEl = document.getElementById("ast-output");

// Decode source from URL hash or use default
function getInitialSource() {
    const hash = location.hash;
    if (hash.startsWith("#source=")) {
        try {
            return atob(hash.slice(8));
        } catch { /* fall through */ }
    }
    const el = document.getElementById("default-source");
    return el ? el.textContent.trim() : "";
}

// Init CodeMirror
const source = getInitialSource();
editor = new EditorView({
    state: EditorState.create({
        doc: source,
        extensions: [
            lineNumbers(),
            highlightActiveLine(),
            highlightActiveLineGutter(),
            history(),
            keymap.of([...defaultKeymap, ...historyKeymap]),
            StreamLanguage.define(snglMode),
            oneDark,
            EditorView.updateListener.of((update) => {
                if (update.docChanged) scheduleCompile();
            }),
        ],
    }),
    parent: document.getElementById("editor-pane"),
});

// Tab switching
document.querySelector(".tab-bar").addEventListener("click", (e) => {
    const tab = e.target.dataset?.tab;
    if (!tab) return;
    activeTab = tab;
    document.querySelectorAll(".tab-bar button").forEach(b =>
        b.classList.toggle("active", b.dataset.tab === tab));
    document.querySelectorAll(".tab-panel").forEach(p =>
        p.classList.toggle("active", p.id === `tab-${tab}`));
    if (tab === "ast") renderAST();
});

// Examples dropdown
document.getElementById("examples").addEventListener("change", (e) => {
    if (e.target.value === "default") {
        const el = document.getElementById("default-source");
        if (el) {
            editor.dispatch({
                changes: { from: 0, to: editor.state.doc.length, insert: el.textContent.trim() },
            });
        }
    }
    e.target.value = "";
});

function scheduleCompile() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(doCompile, 300);
}

function doCompile() {
    if (!wasmReady) return;
    const src = editor.state.doc.toString();

    // Update URL hash
    try { history.replaceState(null, "", "#source=" + btoa(src)); } catch { /* ignore */ }

    const result = window.snglCompile(src);
    if (result.error) {
        errorEl.textContent = result.error;
        errorEl.classList.add("visible");
        previewEl.removeAttribute("srcdoc");
    } else {
        errorEl.classList.remove("visible");
        previewEl.srcdoc = result.html;
    }

    if (activeTab === "ast") renderAST();
}

function renderAST() {
    if (!wasmReady) return;
    const src = editor.state.doc.toString();
    const result = window.snglAST(src);
    if (result.error) {
        astEl.textContent = "Error: " + result.error;
    } else {
        astEl.textContent = result.ast;
    }
}

// Load WASM
async function initWasm() {
    const go = new Go();
    const result = await WebAssembly.instantiateStreaming(
        fetch("assets/playground/sngl.wasm"),
        go.importObject,
    );
    go.run(result.instance);
    wasmReady = true;
    statusEl.textContent = "Ready";
    doCompile();
}

initWasm().catch((err) => {
    statusEl.textContent = "WASM load failed";
    errorEl.textContent = err.message;
    errorEl.classList.add("visible");
});
