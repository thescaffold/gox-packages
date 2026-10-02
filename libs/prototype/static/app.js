/* The prototype's behaviour. A classic script: no modules, no network, no storage. Everything it shows comes from
   window.PROTO (assets/data.js) and lives in memory, so a reload starts again. */
(function () {
  'use strict';
  var P = window.PROTO;
  var app = document.getElementById('app');
  var state = { who: '', rows: {}, log: {}, done: {}, adding: {} };

  P.entities.forEach(function (e) {
    state.rows[e.id] = e.rows.map(function (r) { return Object.assign({}, r); });
  });

  function h(tag, attrs) {
    var el = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === false || v == null) return;
        if (k === 'class') el.className = v;
        else if (k.slice(0, 2) === 'on') el.addEventListener(k.slice(2), v);
        else if (k === 'text') el.textContent = v;
        else el.setAttribute(k, v === true ? '' : v);
      });
    }
    for (var i = 2; i < arguments.length; i++) {
      var c = arguments[i];
      if (c == null || c === false) continue;
      if (Array.isArray(c)) c.forEach(function (x) { if (x != null && x !== false) el.appendChild(typeof x === 'string' ? document.createTextNode(x) : x); });
      else el.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return el;
  }

  function byId(list, id) { return list.filter(function (x) { return x.id === id; })[0]; }
  function visibleScreens() {
    return P.screens.filter(function (s) { return !state.who || s.actors.length === 0 || s.actors.indexOf(state.who) >= 0; });
  }

  // ---- the top bar ----
  function top(route) {
    var links = [['#/', 'Overview', 'home'], ['#/screens', 'Screens', 'screens'], ['#/data', 'Data', 'data'], ['#/features', 'Features', 'features']];
    var who = h('select', { id: 'who', 'aria-label': 'Viewing as', onchange: function (e) { state.who = e.target.value; render(); } },
      h('option', { value: '' }, 'Everyone'),
      P.actors.map(function (a) { return h('option', { value: a.id, selected: state.who === a.id }, a.name); }));
    return h('header', { class: 'top' },
      h('span', { class: 'brand' }, P.title),
      h('nav', { class: 'main', 'aria-label': 'Prototype' }, links.map(function (l) {
        return h('a', { href: l[0], 'aria-current': route.name === l[2] || (l[2] === 'screens' && route.name === 'screen') ? 'page' : false }, l[1]);
      })),
      P.actors.length ? h('label', { class: 'who' }, 'Viewing as ', who) : null);
  }

  // ---- values ----
  function show(f, v, depth) {
    if (v === true) return 'Yes';
    if (v === false) return 'No';
    if (v == null || v === '') return '—';
    if (f.type === 'money') { var n = Number(v); return isNaN(n) ? String(v) : n.toLocaleString('en', { minimumFractionDigits: 2, maximumFractionDigits: 2 }); }
    if (f.type === 'reference') {
      var ref = byId(P.entities, f.ref);
      var ids = Array.isArray(v) ? v : [v];
      return ids.map(function (id) { var row = ref && state.rows[ref.id].filter(function (r) { return r.id === id; })[0]; return row ? label(ref, row, depth) : id; }).join(', ');
    }
    return String(v);
  }
  function label(e, row, depth) {
    var f = e.fields.filter(function (x) { return x.type === 'text'; })[0] || e.fields[0];
    if (!f) return row.id;
    var v = row[f.name];
    if (v == null || v === '') return e.name + ' ' + String(row.id).split('-').pop();
    if (f.type === 'reference' && (depth || 0) < 2) return show(f, v, (depth || 0) + 1);
    return String(v);
  }

  // ---- pages ----
  function overview() {
    return h('div', null,
      h('h1', { tabindex: '-1' }, P.title),
      P.summary ? h('p', { class: 'muted' }, P.summary) : null,
      h('p', null, h('span', { class: 'badge' }, 'Prototype'), ' Made-up data. Nothing here is saved or sent anywhere.'),
      P.actors.length ? h('h2', null, 'Who it is for') : null,
      h('div', { class: 'grid' }, P.actors.map(function (a) {
        return h('div', { class: 'card' }, h('h3', null, a.name), h('p', { class: 'muted' }, a.summary || ''),
          h('button', { class: 'link', onclick: function () { state.who = a.id; render(); } }, 'See it as ' + a.name));
      })),
      h('h2', null, 'Screens'),
      screenGrid());
  }

  function screenGrid() {
    var list = visibleScreens();
    if (!list.length) return h('div', { class: 'empty' }, 'No screens for this person yet.');
    return h('div', { class: 'grid' }, list.map(function (s) {
      return h('div', { class: 'card' }, h('a', { class: 'stretch', href: '#/screen/' + s.id }, h('h3', null, s.name), h('p', { class: 'muted' }, s.summary || ''),
        s.device ? h('span', { class: 'badge' }, s.device) : null,
        s.actors.map(function (id) { var a = byId(P.actors, id); return a ? h('span', { class: 'badge' }, a.name) : null; })));
    }));
  }

  function screens() { return h('div', null, h('h1', { tabindex: '-1' }, 'Screens'), screenGrid()); }

  function toast(text) {
    var old = document.getElementById('toast');
    if (old) old.remove();
    var t = h('div', { class: 'toast', id: 'toast', role: 'status' }, text);
    document.body.appendChild(t);
    setTimeout(function () { if (t.parentNode) t.remove(); }, 2600);
  }

  function logOf(screen) { return state.log[screen.id] || (state.log[screen.id] = []); }

  function entityBlock(screen, e) {
    var rows = state.rows[e.id];
    var table = rows.length
      ? h('div', { class: 'scroll' }, h('table', null,
          h('thead', null, h('tr', null, e.fields.map(function (f) { return h('th', { scope: 'col' }, f.name); }), h('th', { scope: 'col' }, h('span', { class: 'sr' }, 'Remove')))),
          h('tbody', null, rows.map(function (r) {
            return h('tr', null, e.fields.map(function (f) { return h('td', null, show(f, r[f.name])); }),
              h('td', null, h('button', { class: 'link', 'aria-label': 'Remove ' + label(e, r), onclick: function () {
                state.rows[e.id] = rows.filter(function (x) { return x !== r; }); logOf(screen).push('Removed ' + label(e, r)); render(); } }, 'Remove')));
          }))))
      : h('div', { class: 'empty' }, 'Nothing here yet.');
    var adding = state.adding[screen.id + ':' + e.id];
    return h('section', null,
      h('h2', null, e.name),
      e.summary ? h('p', { class: 'muted' }, e.summary) : null,
      table,
      h('div', { class: 'actions' }, h('button', { onclick: function () { state.adding[screen.id + ':' + e.id] = !adding; render(); } }, adding ? 'Cancel' : 'Add ' + e.name.toLowerCase())),
      adding ? addForm(screen, e) : null);
  }

  function input(e, f) {
    var id = 'f-' + e.id + '-' + f.name.replace(/\W+/g, '-');
    var control;
    switch (f.type) {
      case 'number': case 'money': control = h('input', { id: id, name: f.name, type: 'number', step: f.type === 'money' ? '0.01' : '1' }); break;
      case 'date': control = h('input', { id: id, name: f.name, type: 'date' }); break;
      case 'time': control = h('input', { id: id, name: f.name, type: 'time' }); break;
      case 'email': control = h('input', { id: id, name: f.name, type: 'email' }); break;
      case 'phone': control = h('input', { id: id, name: f.name, type: 'tel' }); break;
      case 'yes/no': control = h('select', { id: id, name: f.name }, h('option', { value: 'yes' }, 'Yes'), h('option', { value: 'no' }, 'No')); break;
      case 'one of': control = h('select', { id: id, name: f.name }, (f.options || []).map(function (o) { return h('option', { value: o }, o); })); break;
      case 'reference':
        var ref = byId(P.entities, f.ref);
        control = ref ? h('select', { id: id, name: f.name }, state.rows[ref.id].map(function (r) { return h('option', { value: r.id }, label(ref, r)); })) : h('input', { id: id, name: f.name, type: 'text' }); break;
      default: control = h('input', { id: id, name: f.name, type: 'text' });
    }
    return h('label', { for: id }, f.name, control);
  }

  function addForm(screen, e) {
    return h('form', { class: 'add', onsubmit: function (ev) {
      ev.preventDefault();
      var row = { id: e.id + '-new-' + (state.rows[e.id].length + 1) };
      e.fields.forEach(function (f) {
        var el = ev.target.elements[f.name];
        var v = el ? el.value : '';
        if (f.type === 'yes/no') v = v === 'yes';
        if (f.type === 'number' || f.type === 'money') v = v === '' ? null : Number(v);
        row[f.name] = v;
      });
      state.rows[e.id].push(row);
      state.adding[screen.id + ':' + e.id] = false;
      logOf(screen).push('Added ' + label(e, row));
      toast(e.name + ' added (only in this prototype)');
      render();
    } }, e.fields.map(function (f) { return input(e, f); }),
      h('div', { class: 'row' }, h('button', { class: 'primary', type: 'submit' }, 'Save'), h('button', { type: 'button', onclick: function () { state.adding[screen.id + ':' + e.id] = false; render(); } }, 'Cancel')));
  }

  function screenPage(id) {
    var s = byId(P.screens, id);
    if (!s) return notFound();
    var body = h('div', null,
      h('p', null, h('a', { href: '#/screens' }, '← All screens')),
      h('h1', { tabindex: '-1' }, s.name),
      s.summary ? h('p', { class: 'muted' }, s.summary) : null,
      h('p', null, s.device ? h('span', { class: 'badge' }, s.device) : null,
        s.actors.map(function (a) { var x = byId(P.actors, a); return x ? h('span', { class: 'badge' }, 'for ' + x.name) : null; })),
      s.actions.length ? h('div', { class: 'actions', role: 'group', 'aria-label': 'Actions' }, s.actions.map(function (a) {
        return h('button', { class: 'primary', onclick: function () { logOf(s).push(a); toast('“' + a + '” — in the real app this happens here.'); render(); } }, a);
      })) : null,
      s.entities.map(function (eid) { var e = byId(P.entities, eid); return e ? entityBlock(s, e) : null; }),
      s.features.length ? h('h2', null, 'Features on this screen') : null,
      s.features.length ? h('ul', null, s.features.map(function (fid) { var f = byId(P.features, fid); return f ? h('li', null, h('a', { href: '#/features' }, f.name)) : null; })) : null,
      logOf(s).length ? h('div', null, h('h2', null, 'What you did here'), h('ul', { class: 'log' }, logOf(s).map(function (t) { return h('li', null, t); }))) : null);
    return s.device === 'mobile' ? h('div', { class: 'phone' }, body) : body;
  }

  function dataPage() {
    return h('div', null, h('h1', { tabindex: '-1' }, 'Data'), h('p', { class: 'muted' }, 'What the system keeps. The rows are made up.'),
      P.entities.length ? P.entities.map(function (e) {
        return h('section', null, h('h2', null, e.name), e.summary ? h('p', { class: 'muted' }, e.summary) : null,
          h('div', { class: 'scroll' }, h('table', null, h('thead', null, h('tr', null, h('th', { scope: 'col' }, 'Field'), h('th', { scope: 'col' }, 'Kind'))),
            h('tbody', null, e.fields.map(function (f) {
              return h('tr', null, h('td', null, f.name), h('td', null, f.type === 'one of' ? 'one of ' + (f.options || []).join(', ') : f.type === 'reference' ? (f.many ? 'several ' : 'one ') + (f.ref ? (byId(P.entities, f.ref) || { name: f.ref }).name : '') : f.type));
            })))));
      }) : h('div', { class: 'empty' }, 'The spec describes no data yet.'));
  }

  function featuresPage() {
    return h('div', null, h('h1', { tabindex: '-1' }, 'Features'),
      P.features.length ? P.features.map(function (f) {
        return h('section', { class: 'card sect' }, h('h3', null, f.name), f.priority ? h('span', { class: 'badge ' + f.priority }, f.priority) : null,
          f.summary ? h('p', { class: 'muted' }, f.summary) : null,
          f.rules.length ? h('div', null, h('strong', null, 'It must'), h('ul', null, f.rules.map(function (r) { return h('li', null, r.text); }))) : null,
          f.criteria.length ? h('div', null, h('strong', null, 'Done when'), h('ul', { class: 'checks' }, f.criteria.map(function (c) {
            var id = 'c-' + f.id + '-' + c.id;
            return h('li', null, h('input', { type: 'checkbox', id: id, checked: !!state.done[c.id], onchange: function (e) { state.done[c.id] = e.target.checked; } }), h('label', { for: id }, c.text));
          }))) : null);
      }) : h('div', { class: 'empty' }, 'The spec describes no features yet.'),
      P.limits.length ? h('div', null, h('h2', null, 'Limits'), h('ul', null, P.limits.map(function (l) { return h('li', null, (l.kind ? l.kind + ': ' : '') + l.text); }))) : null);
  }

  function notFound() { return h('div', { class: 'empty' }, h('h1', { tabindex: '-1' }, 'Not found'), h('p', null, 'There is no such page in this prototype. ', h('a', { href: '#/' }, 'Back to the start'))); }

  // ---- routing ----
  function route() {
    var parts = (location.hash || '#/').replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
    if (!parts.length) return { name: 'home' };
    if (parts[0] === 'screens') return { name: 'screens' };
    if (parts[0] === 'screen' && parts[1]) return { name: 'screen', id: parts[1] };
    if (parts[0] === 'data') return { name: 'data' };
    if (parts[0] === 'features') return { name: 'features' };
    return { name: 'none' };
  }

  var last = '';
  function render() {
    var r = route();
    var page = r.name === 'home' ? overview() : r.name === 'screens' ? screens() : r.name === 'screen' ? screenPage(r.id) : r.name === 'data' ? dataPage() : r.name === 'features' ? featuresPage() : notFound();
    var focused = document.activeElement && document.activeElement.id;
    app.textContent = '';
    app.appendChild(top(r));
    app.appendChild(h('main', { id: 'main' }, page));
    var key = location.hash;
    if (key !== last) {
      last = key;
      var heading = app.querySelector('h1');
      if (heading) heading.focus();
      window.scrollTo(0, 0);
    } else if (focused) {
      var again = document.getElementById(focused);
      if (again) again.focus();
    }
    document.title = P.title + ' — prototype';
  }

  window.addEventListener('hashchange', render);
  render();
})();
