package main

const wrapperHTML = `<!DOCTYPE html>
<html><head>
<meta charset="utf-8">
<title>SNGL Preview</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: system-ui, sans-serif; height: 100vh; display: flex; flex-direction: column; background: #1a1a2e; color: #e0e0e0; }
#toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 16px; background: #16213e; border-bottom: 1px solid #0f3460; }
#toolbar label { font-weight: 600; font-size: 14px; color: #e94560; }
#toolbar select { background: #0f3460; color: #e0e0e0; border: 1px solid #533483; padding: 4px 8px; border-radius: 4px; font-size: 13px; }
#toolbar .filename { font-size: 13px; color: #999; margin-left: auto; }
#main { display: flex; flex: 1; overflow: hidden; }
#preview-frame { flex: 1; border: none; background: #fff; }
#sidebar { width: 280px; background: #16213e; border-left: 1px solid #0f3460; display: flex; flex-direction: column; overflow-y: auto; }
#sidebar-header { padding: 12px 16px; font-weight: 600; font-size: 14px; border-bottom: 1px solid #0f3460; }
#sidebar-content { padding: 12px 16px; font-size: 13px; }
#sidebar-content .empty { color: #666; font-style: italic; }
.prop-group { margin-bottom: 12px; }
.prop-group h4 { font-size: 11px; text-transform: uppercase; color: #999; margin-bottom: 6px; }
.prop-row { display: flex; align-items: center; gap: 8px; margin-bottom: 4px; }
.prop-row .prop-name { color: #aaa; min-width: 80px; font-size: 12px; }
.prop-row.unset .prop-name { color: #555; }
.prop-row input, .prop-row select { flex: 1; background: #0f3460; color: #e0e0e0; border: 1px solid #533483; padding: 3px 6px; border-radius: 3px; font-size: 12px; font-family: monospace; }
.prop-row.unset input, .prop-row.unset select { color: #666; border-color: #2a2a4a; }
.prop-row .prop-type { color: #444; font-size: 10px; min-width: 32px; text-align: right; }
.prop-row .event-dot { width: 6px; height: 6px; border-radius: 50%; display: inline-block; }
.prop-row .event-dot.set { background: #4ade80; }
.prop-row .event-dot.unset { background: #333; }
</style>
</head><body>
<div id="toolbar">
  <label>SNGL Preview</label>
  <select id="target"></select>
  <span class="filename" id="filename"></span>
</div>
<div id="main">
  <iframe id="preview-frame" src="/preview"></iframe>
  <div id="sidebar">
    <div id="sidebar-header">Properties</div>
    <div id="sidebar-content"><div class="empty">Click an element to inspect</div></div>
  </div>
</div>
<script>
const targetSelect = document.getElementById('target');
const sidebar = document.getElementById('sidebar-content');
const filenameEl = document.getElementById('filename');

// Load targets
fetch('/targets').then(r => r.json()).then(targets => {
  filenameEl.textContent = targets.file || '';
  targets.targets.forEach(t => {
    const opt = document.createElement('option');
    opt.value = t.platform + '/' + t.lang;
    opt.textContent = t.platform + ' (' + t.lang + ')';
    if (t.active) opt.selected = true;
    targetSelect.appendChild(opt);
  });
});

targetSelect.addEventListener('change', function() {
  const [platform, lang] = this.value.split('/');
  fetch('/switch?platform=' + encodeURIComponent(platform) + '&lang=' + encodeURIComponent(lang))
    .then(() => {});
});

let selectedLine = 0, selectedCol = 0;

window.addEventListener('message', function(e) {
  if (!e.data) return;
  if (e.data.type === 'sngl-deselect') {
    selectedLine = 0;
    selectedCol = 0;
    loadAppSidebar();
    return;
  }
  if (e.data.type === 'sngl-state') {
    fetch('/app/state', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({state: e.data.state}),
    }).then(() => {
      if (!selectedLine || !selectedCol) loadAppSidebar();
    });
    return;
  }
  if (e.data.type === 'sngl-reload') {
    if (selectedLine && selectedCol) {
      fetch('/node?line=' + selectedLine + '&col=' + selectedCol)
        .then(r => r.json()).then(renderSidebar).catch(() => {});
    } else {
      loadAppSidebar();
    }
    return;
  }
  if (e.data.type !== 'sngl-select') return;
  selectedLine = e.data.line;
  selectedCol = e.data.col;
  fetch('/node?line=' + e.data.line + '&col=' + e.data.col)
    .then(r => r.json())
    .then(renderSidebar)
    .catch(() => { sidebar.innerHTML = '<div class="empty">Could not load node</div>'; });
});

function loadAppSidebar() {
  fetch('/app').then(r => r.json()).then(renderAppSidebar)
    .catch(() => { sidebar.innerHTML = '<div class="empty">Could not load app state</div>'; });
}

function renderAppSidebar(app) {
  var html = '<div class="prop-group"><h4>App State</h4></div>';
  if (app.data && app.data.length > 0) {
    html += '<div class="prop-group"><h4>Data</h4>';
    app.data.forEach(function(d) {
      html += '<div class="prop-row"><span class="prop-name">' + escapeHtml(d.name) + '</span>';
      var val = d.value != null ? String(d.value) : (d.init || '');
      html += '<input value="' + escapeHtml(val) + '" readonly />';
      if (d.type) html += '<span class="prop-type">' + escapeHtml(d.type) + '</span>';
      html += '</div>';
    });
    html += '</div>';
  }
  if (app.computed && app.computed.length > 0) {
    html += '<div class="prop-group"><h4>Computed</h4>';
    app.computed.forEach(function(c) {
      html += '<div class="prop-row"><span class="prop-name">' + escapeHtml(c.name) + '</span>';
      html += '<input value="' + escapeHtml(c.expr || '') + '" readonly />';
      html += '</div>';
    });
    html += '</div>';
  }
  if (!app.data.length && !app.computed.length) {
    html += '<div class="empty">No app state defined</div>';
  }
  sidebar.innerHTML = html;
}

loadAppSidebar();

function propValue(v) {
  if (v.cel) return v.cel;
  if (v.literal != null) return String(v.literal);
  return '';
}

function renderPropInput(k, v, kind) {
  var ek = escapeHtml(k);
  var cls = v.set ? '' : ' unset';
  var row = '<div class="prop-row' + cls + '"><span class="prop-name">' + ek + '</span>';
  if (v.enum && v.enum.length > 0) {
    row += '<select data-kind="' + kind + '" data-key="' + ek + '">';
    row += '<option value=""' + (propValue(v) === '' ? ' selected' : '') + '></option>';
    v.enum.forEach(function(opt) {
      var sel = propValue(v) === opt ? ' selected' : '';
      row += '<option value="' + escapeHtml(opt) + '"' + sel + '>' + escapeHtml(opt) + '</option>';
    });
    row += '</select>';
  } else {
    row += '<input data-kind="' + kind + '" data-key="' + ek + '" value="' + escapeHtml(propValue(v)) + '"';
    if (v.cel) row += ' title="CEL expression" readonly';
    row += ' />';
  }
  if (v.type && v.type !== 'style') row += '<span class="prop-type">' + escapeHtml(v.type) + '</span>';
  row += '</div>';
  return row;
}

function renderSidebar(node) {
  var html = '<div class="prop-group"><h4>' + escapeHtml(node.component) + ' (' + node.pos.line + ':' + node.pos.col + ')</h4></div>';

  if (node.props && Object.keys(node.props).length > 0) {
    // Set props first, then unset
    var setProps = [], unsetProps = [];
    for (var k in node.props) {
      if (node.props[k].set) setProps.push(k); else unsetProps.push(k);
    }
    html += '<div class="prop-group"><h4>Props</h4>';
    setProps.forEach(function(k) { html += renderPropInput(k, node.props[k], 'prop'); });
    unsetProps.sort().forEach(function(k) { html += renderPropInput(k, node.props[k], 'prop'); });
    html += '</div>';
  }

  if (node.styles && Object.keys(node.styles).length > 0) {
    var setStyles = [], unsetStyles = [];
    for (var k in node.styles) {
      if (node.styles[k].set) setStyles.push(k); else unsetStyles.push(k);
    }
    html += '<div class="prop-group"><h4>Styles (' + setStyles.length + '/' + (setStyles.length + unsetStyles.length) + ')</h4>';
    setStyles.forEach(function(k) { html += renderPropInput(k, node.styles[k], 'style'); });
    unsetStyles.forEach(function(k) { html += renderPropInput(k, node.styles[k], 'style'); });
    html += '</div>';
  }

  if (node.events && node.events.length > 0) {
    html += '<div class="prop-group"><h4>Events</h4>';
    node.events.forEach(function(ev) {
      var cls = ev.set ? '' : ' unset';
      var dot = ev.set ? 'set' : 'unset';
      html += '<div class="prop-row' + cls + '"><span class="event-dot ' + dot + '"></span><span class="prop-name">' + escapeHtml(ev.name) + '</span></div>';
    });
    html += '</div>';
  }

  sidebar.innerHTML = html;

  // Attach change handlers for editing
  sidebar.querySelectorAll('[data-kind]').forEach(function(input) {
    input.addEventListener('change', function() {
      var body = {};
      if (this.dataset.kind === 'prop') {
        body.props = {};
        body.props[this.dataset.key] = this.value;
      } else {
        body.styles = {};
        body.styles[this.dataset.key] = this.value;
      }
      fetch('/node?line=' + selectedLine + '&col=' + selectedCol, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body),
      });
    });
  });
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}
</script>
</body></html>`

const liveReloadScript = `<script>
(function() {
  var es = new EventSource('/events');
  es.addEventListener('reload', function() {
    var saved = (typeof state !== 'undefined') ? JSON.parse(JSON.stringify(state)) : null;
    fetch('/preview?raw=1').then(function(r) { return r.text(); }).then(function(html) {
      var parser = new DOMParser();
      var newDoc = parser.parseFromString(html, 'text/html');
      document.head.innerHTML = newDoc.head.innerHTML;
      document.body.innerHTML = newDoc.body.innerHTML;
      document.body.querySelectorAll('script').forEach(function(s) {
        var ns = document.createElement('script');
        ns.textContent = s.textContent;
        s.replaceWith(ns);
      });
      if (saved && typeof state !== 'undefined') {
        Object.keys(saved).forEach(function(k) { if (k in state) state[k] = saved[k]; });
      }
      if (typeof __sngl_updates !== 'undefined') __sngl_updates.forEach(function(fn) { fn(); });
      window.parent.postMessage({type: 'sngl-reload'}, '*');
    });
  });

  var selected = null;
  document.addEventListener('click', function(e) {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    e.stopPropagation();
    var el = e.target;
    while (el && !el.dataset.snglLine) el = el.parentElement;
    if (selected) selected.style.outline = '';
    if (!el) {
      selected = null;
      window.parent.postMessage({type: 'sngl-deselect'}, '*');
      return;
    }
    selected = el;
    selected.style.outline = '2px solid #4a9eff';
    window.parent.postMessage({
      type: 'sngl-select',
      line: parseInt(el.dataset.snglLine),
      col: parseInt(el.dataset.snglCol)
    }, '*');
  }, true);
})();
</script>`
