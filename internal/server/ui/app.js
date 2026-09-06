// SPDX-License-Identifier: Apache-2.0
//
// Read-only viewer for ManagedNamespaces and ClusterAccessMappings. Every tab is
// derived from the list endpoints; nothing is mutated. All cluster-supplied
// strings are rendered via textContent to avoid injection.
'use strict';

var namespaces = [];
var cams = [];
function $(id) { return document.getElementById(id); }

function el(tag, text, cls) {
  var e = document.createElement(tag);
  if (text != null) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}
function row(cells, head) {
  var tr = document.createElement('tr');
  cells.forEach(function (c) {
    var cell = document.createElement(head ? 'th' : 'td');
    if (c && c.nodeType) cell.appendChild(c); else cell.textContent = c;
    tr.appendChild(cell);
  });
  return tr;
}
function table(head, rows) {
  var t = document.createElement('table');
  t.appendChild(row(head, true));
  rows.forEach(function (r) { t.appendChild(row(r)); });
  return t;
}
async function getJSON(url) {
  var r = await fetch(url);
  var t = await r.text();
  if (!r.ok) throw new Error(t || r.status);
  return JSON.parse(t) || [];
}

async function loadData() {
  var both = await Promise.all([
    getJSON('/api/v1/managednamespaces'),
    getJSON('/api/v1/clusteraccessmappings')
  ]);
  namespaces = both[0];
  cams = both[1];
}

function tab(name) {
  ['ns', 'cluster', 'search'].forEach(function (t) {
    $('tab-' + t).classList.toggle('active', name === t);
    $('view-' + t).hidden = name !== t;
  });
}

function nsName(n) { return n.metadata.name; }
function mappings(n) { return (n.spec && n.spec.accessMappings) || []; }

// Status. The operator reports two separate things, and a viewer that shows
// only the spec would misrepresent both: whether the last pass completed at
// all, and which ClusterRole references it could not resolve. A mapping naming
// a missing ClusterRole is written nowhere and grants nothing, so listing it
// like any other row would show access that does not exist.

function readyCondition(o) {
  var cs = (o.status && o.status.conditions) || [];
  for (var i = 0; i < cs.length; i++) if (cs[i].type === 'Ready') return cs[i];
  return null;
}

// invalidRoles maps each unresolved ClusterRole name to why it failed.
function invalidRoles(o) {
  var out = Object.create(null);
  ((o.status && o.status.invalidReferences) || []).forEach(function (r) {
    if (r.clusterRole) out[r.clusterRole] = r.reason || 'invalid reference';
  });
  return out;
}

// health reduces the Ready condition to one verdict. A condition older than the
// spec it describes is reported as pending rather than as fact, so a stale pass
// is never read as agreement with what is on screen.
function health(o) {
  var c = readyCondition(o);
  if (!c) return { state: 'pending', label: 'Pending', detail: 'Not reconciled yet.' };
  var gen = o.metadata.generation;
  if (gen != null && c.observedGeneration != null && c.observedGeneration < gen) {
    return {
      state: 'pending', label: 'Pending',
      detail: 'A newer change has not been reconciled yet. Last completed pass: ' + (c.message || c.reason)
    };
  }
  if (c.status !== 'True') {
    return { state: 'bad', label: 'Not ready', detail: (c.reason || 'Failed') + (c.message ? ': ' + c.message : '') };
  }
  return { state: 'ok', label: 'Ready', detail: c.message || 'Reconciled.' };
}

function badge(h) {
  var b = el('span', h.label, 'badge ' + h.state);
  b.title = h.detail;
  return b;
}

// rolesCell renders a mapping's ClusterRoles, striking through any the operator
// could not resolve.
function rolesCell(roles, invalid) {
  var wrap = document.createElement('span');
  (roles || []).forEach(function (r, i) {
    if (i) wrap.appendChild(document.createTextNode(', '));
    var why = invalid[r];
    var e = el('span', r, why ? 'role-bad' : null);
    if (why) e.title = why + ' — this grant is not in effect';
    wrap.appendChild(e);
  });
  return wrap;
}

function statusBlock(o) {
  var wrap = document.createElement('div');
  var h = health(o);
  var line = el('p', null, 'status-line');
  line.appendChild(badge(h));
  line.appendChild(el('span', h.detail, 'muted'));
  wrap.appendChild(line);
  var bad = (o.status && o.status.invalidReferences) || [];
  if (bad.length) {
    wrap.appendChild(table(['Unresolved ClusterRole', 'Reason'], bad.map(function (r) {
      return [r.clusterRole || '(unnamed)', r.reason];
    })));
  }
  return wrap;
}

function subjectCells(m, invalid) {
  var kind = m.group ? 'Group' : 'User';
  var subject = m.group ? m.group : (m.users || []).join(', ');
  return [subject, kind, rolesCell(m.clusterRoles, invalid || Object.create(null))];
}

function renderList() {
  var list = $('ns-list');
  list.textContent = '';
  if (!namespaces.length) { list.appendChild(el('p', 'No managed namespaces.', 'muted')); return; }
  namespaces.slice().sort(function (a, b) { return nsName(a) < nsName(b) ? -1 : 1; }).forEach(function (n) {
    var name = nsName(n);
    var b = el('button', null, 'ns-item');
    b.appendChild(el('span', name));
    var h = health(n);
    if (h.state !== 'ok') b.appendChild(badge(h));
    b.onclick = function () {
      Array.prototype.forEach.call(document.querySelectorAll('.ns-item'), function (x) { x.classList.remove('sel'); });
      b.classList.add('sel');
      renderDetail(name);
    };
    list.appendChild(b);
  });
}

function quotaScopeLines(q) {
  var lines = (q.scopes || []).slice();
  ((q.scopeSelector && q.scopeSelector.matchExpressions) || []).forEach(function (e) {
    var s = e.scopeName + ' ' + e.operator;
    if (e.values && e.values.length) s += ' [' + e.values.join(', ') + ']';
    lines.push(s);
  });
  return lines;
}

function renderQuota(q) {
  var wrap = document.createElement('div');
  wrap.appendChild(el('h5', q.name || '(unnamed)'));
  var scopes = quotaScopeLines(q);
  if (scopes.length) wrap.appendChild(el('p', 'Scopes: ' + scopes.join(', '), 'muted'));
  var hard = q.hard;
  if (hard && Object.keys(hard).length) {
    wrap.appendChild(table(['Resource', 'Hard limit'],
      Object.keys(hard).sort().map(function (k) { return [k, String(hard[k])]; })));
  } else {
    wrap.appendChild(el('p', 'No hard limits', 'muted'));
  }
  return wrap;
}

function renderDetail(name) {
  var n = namespaces.find(function (x) { return nsName(x) === name; });
  var d = $('ns-detail');
  d.textContent = '';
  if (!n) return;
  d.appendChild(el('h3', name));
  d.appendChild(statusBlock(n));

  d.appendChild(el('h4', 'ResourceQuotas'));
  var quotas = (n.spec && n.spec.resourceQuotas) || [];
  if (!quotas.length) {
    d.appendChild(el('p', 'None', 'muted'));
  } else {
    quotas.forEach(function (q) { d.appendChild(renderQuota(q)); });
  }

  d.appendChild(el('h4', 'Permissions'));
  var ms = mappings(n);
  if (!ms.length) { d.appendChild(el('p', 'None', 'muted')); return; }
  var invalid = invalidRoles(n);
  d.appendChild(table(['Subject', 'Kind', 'ClusterRoles'], ms.map(function (m) {
    return subjectCells(m, invalid);
  })));
}

function renderCluster() {
  var out = $('cluster-out');
  out.textContent = '';
  out.appendChild(el('p', 'Cluster-wide grants via ClusterRoleBindings.', 'muted'));
  if (!cams.length) { out.appendChild(el('p', 'No cluster access mappings.', 'muted')); return; }
  var rows = cams.slice().sort(function (a, b) { return a.metadata.name < b.metadata.name ? -1 : 1; })
    .map(function (c) {
      return [c.metadata.name]
        .concat(subjectCells(c.spec || {}, invalidRoles(c)))
        .concat([badge(health(c))]);
    });
  out.appendChild(table(['Name', 'Subject', 'Kind', 'ClusterRoles', 'Status'], rows));
}

// match returns the union of ClusterRoles the query is granted by the given
// mappings, plus how it matched (user and/or group).
function match(ms, q) {
  var roles = Object.create(null), kinds = Object.create(null);
  ms.forEach(function (m) {
    var hit = false;
    if (m.group && m.group === q) { hit = true; kinds.group = true; }
    if ((m.users || []).indexOf(q) >= 0) { hit = true; kinds.user = true; }
    if (hit) (m.clusterRoles || []).forEach(function (r) { roles[r] = true; });
  });
  var list = Object.keys(roles).sort();
  return { roles: list, kinds: Object.keys(kinds).sort().join(', '), any: list.length > 0 };
}

function search() {
  var q = $('q').value.trim();
  var out = $('search-out');
  out.textContent = '';
  if (!q) return;
  var rows = [];
  namespaces.forEach(function (n) {
    var m = match(mappings(n), q);
    if (m.any) rows.push([nsName(n), m.kinds, rolesCell(m.roles, invalidRoles(n)), badge(health(n))]);
  });
  cams.forEach(function (c) {
    var m = match([c.spec || {}], q);
    if (m.any) rows.push(['cluster-wide', m.kinds, rolesCell(m.roles, invalidRoles(c)), badge(health(c))]);
  });
  if (!rows.length) { out.appendChild(el('p', 'No access found for "' + q + '".', 'muted')); return; }
  rows.sort(function (a, b) { return a[0] < b[0] ? -1 : 1; });
  out.appendChild(el('p', 'A struck-through ClusterRole is named by the spec but could not be resolved, so it grants nothing.', 'muted'));
  out.appendChild(table(['Scope', 'Matched as', 'ClusterRoles', 'Status'], rows));
}

async function refresh() {
  try {
    await loadData();
    renderList();
    renderCluster();
    $('ns-detail').textContent = '';
    $('ns-detail').appendChild(el('p', 'Select a namespace.', 'muted'));
    $('search-out').textContent = '';
  } catch (e) {
    $('ns-list').textContent = '';
    $('ns-list').appendChild(el('p', e.message, 'muted'));
  }
}

refresh();
