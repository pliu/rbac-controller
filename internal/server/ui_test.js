// SPDX-License-Identifier: Apache-2.0
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const app = vm.createContext({
  // Leave the initial refresh pending; these tests exercise rendering/search
  // directly with supplied API data and need no network or browser.
  fetch: () => new Promise(() => {}),
  document: {
    createElement: tag => ({tag, children: [], appendChild(child) { this.children.push(child); }}),
    createTextNode: text => ({textContent: text})
  }
});
vm.runInContext(fs.readFileSync(path.join(__dirname, 'ui/app.js'), 'utf8'), app);

test('prototype-named ClusterRoles render as granted unless explicitly invalid', () => {
  for (const name of ['constructor', '__proto__', 'toString', 'view']) {
    const valid = app.rolesCell([name], app.invalidRoles({})).children[0];
    assert.equal(valid.textContent, name);
    assert.equal(valid.className, undefined);
    assert.equal(valid.title, undefined);

    const invalid = app.invalidRoles({status: {invalidReferences: [{clusterRole: name, reason: 'ClusterRole not found'}]}});
    const bad = app.rolesCell([name], invalid).children[0];
    assert.equal(bad.className, 'role-bad');
    assert.equal(bad.title, 'ClusterRole not found — this grant is not in effect');
  }
});

test('subject cells without status do not mark constructor invalid', () => {
  const roles = app.subjectCells({group: 'devs', clusterRoles: ['constructor']})[2];
  assert.equal(roles.children[0].className, undefined);
});

test('search includes and deduplicates prototype-named ClusterRoles', () => {
  const result = app.match([
    {group: 'devs', clusterRoles: ['constructor', '__proto__', 'view']},
    {users: ['devs'], clusterRoles: ['__proto__']}
  ], 'devs');
  assert.deepEqual(Array.from(result.roles), ['__proto__', 'constructor', 'view']);
  assert.equal(result.any, true);
  assert.equal(result.kinds, 'group, user');
});
