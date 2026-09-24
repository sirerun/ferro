'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

test('side panel accepts local and approved hosted bridge endpoints only', () => {
  const source = fs.readFileSync(path.join(__dirname, 'sidepanel.js'), 'utf8');
  const setup = source.slice(0, source.indexOf('let connection'));
  const context = vm.createContext({document:{getElementById:()=>({})}});
  vm.runInContext(setup, context);
  for (const [endpoint, accepted] of [
    ['http://127.0.0.1:4175', true],
    ['https://ferro.sire.run/bridge', true],
    ['http://ferro.sire.run/bridge', false],
    ['https://ferro.sire.run/bridge/', false],
    ['https://remote.example/bridge', false],
    ['http://remote.example:4175', false],
  ]) {
    assert.equal(vm.runInContext(`validBridgeBase(${JSON.stringify(endpoint)})`, context), accepted, endpoint);
  }
});
