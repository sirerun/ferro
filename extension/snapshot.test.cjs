'use strict';
/* extension/snapshot.test.cjs -- the Node-side half of T12.2's ref-
 * numbering parity test (ADR 006 decision #3; docs/plan.md T12.2's acc
 * line). Run with: FERRO_TEST_BROWSER=1 node --test extension/
 *
 * Mirrors ~/Code/dndungu/ox/extension/adapter.test.cjs's overall shape
 * (node:test + node:assert/strict, execute the real adapter.js source
 * rather than reimplementing its logic, no npm dependency) but evaluates
 * that source inside a real headless Chrome page instead of ox's
 * node:vm + hand-mocked DOM objects. That's a deliberate deviation, not a
 * shortcut: ox's tested functions (verifiedCopy/reconstruct) are pure
 * string/tree manipulation over objects the test constructs by hand, but
 * takeSnapshot() (the function under test here) depends on real
 * getComputedStyle()/getBoundingClientRect() layout results to decide
 * what's visible -- exactly the class of behavior a hand-mocked or jsdom
 * DOM cannot reproduce faithfully (see testsupport/cdp.cjs's header
 * comment for the full reasoning). Real Chrome is the only way this test
 * can fail when the JS port's *visibility* rules actually drift from
 * snapshot.go's, which is the whole point of a parity test.
 *
 * This loads extension/adapter.js's actual file contents (not a
 * reimplementation) into the page and calls its exported
 * FerroAdapter.takeSnapshot() directly -- the same function content.js
 * calls in production.
 */

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { launchChrome, findChrome } = require('./testsupport/cdp.cjs');

const FIXTURE_PATH = path.join(__dirname, 'testdata', 'fixture.html');
const GOLDEN_PATH = path.join(__dirname, '..', 'internal', 'core', 'testdata', 'fixture.golden.json');
const ADAPTER_PATH = path.join(__dirname, 'adapter.js');

// Window size MUST match internal/core/snapshot_parity_test.go's
// chromedp.WindowSize(1280, 3000) call exactly -- see that file's comment.
const WINDOW_SIZE = [1280, 3000];

// maxElements MUST match the Go side's TakeSnapshot(ctx, 200) call.
const MAX_ELEMENTS = 200;

test('extension takeSnapshot() matches internal/core/snapshot.go golden (T12.2 parity)', async (t) => {
  if (process.env.FERRO_TEST_BROWSER !== '1' && process.env.FERRO_TEST_BROWSER !== 'true') {
    t.skip('set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)');
    return;
  }
  if (!findChrome()) {
    t.skip('no Chrome binary found (set CHROME_PATH)');
    return;
  }

  const golden = JSON.parse(fs.readFileSync(GOLDEN_PATH, 'utf8'));
  const adapterSource = fs.readFileSync(ADAPTER_PATH, 'utf8');

  const chrome = await launchChrome({ windowSize: WINDOW_SIZE });
  try {
    await chrome.navigate('file://' + FIXTURE_PATH);
    // Evaluate the real, unmodified adapter.js source -- defines
    // globalThis.FerroAdapter. The `chrome.*` extension APIs it references
    // in perform()'s trusted-input path are never called by
    // takeSnapshot(), so this succeeds even outside an extension context.
    await chrome.evaluate(adapterSource);
    const snapshot = await chrome.evaluate(`FerroAdapter.takeSnapshot(${MAX_ELEMENTS})`);

    assert.ok(snapshot, 'takeSnapshot() returned nothing');
    assert.ok(Array.isArray(snapshot.elements), 'snapshot.elements must be an array');

    // url is deliberately excluded from comparison -- see
    // snapshot_parity_test.go's comparablePart doc comment (it's an
    // absolute file:// path that differs per checkout).
    const comparable = {
      title: snapshot.title,
      elements: snapshot.elements,
    };
    if (snapshot.truncated) comparable.truncated = true;

    assert.deepEqual(
      comparable,
      golden,
      'extension snapshot does not match internal/core/snapshot.go golden -- ' +
        'ref numbering or element selection has drifted between the Go and JS ports'
    );

    const selectors = await chrome.evaluate(`FerroAdapter.takeSnapshot(${MAX_ELEMENTS}, true).elements.map((el) => ({
      selector: el.selector,
      count: document.querySelectorAll(el.selector).length,
    }))`);
    assert.ok(selectors.length > 0);
    assert.ok(selectors.every((entry) => entry.selector && entry.count === 1),
      `execution snapshots must provide a unique live selector for every ref: ${JSON.stringify(selectors.filter((entry) => !entry.selector || entry.count !== 1).slice(0, 3))}`);

    const executionSnapshot = await chrome.evaluate(`FerroAdapter.takeSnapshot(${MAX_ELEMENTS}, true)`);
    const signIn = executionSnapshot.elements.find((element) => element.tag === 'button' && element.text === 'Sign in');
    assert.ok(signIn, 'fixture sign-in button should be present');
    const encodeTarget = (element) => Buffer.from(JSON.stringify({
      selector: element.selector,
      tag: element.tag,
      role: element.role,
      name: element.name,
      text: element.text,
      href: element.href,
    })).toString('base64url');

    const longLabel = `${'x'.repeat(79)}😀`;
    const longText = `${'z'.repeat(79)}😀`;
    await chrome.evaluate(`(() => {
      globalThis.chrome = {runtime:{sendMessage:async()=>({})}};
      const input = document.createElement('input');
      input.type = 'text';
      input.setAttribute('aria-label', ${JSON.stringify(longLabel)});
      document.body.append(input);
      const button = document.createElement('button');
      button.innerText = ${JSON.stringify(longText)};
      document.body.append(button);
    })()`);
    const specialSnapshot = await chrome.evaluate(`FerroAdapter.takeSnapshot(${MAX_ELEMENTS}, true)`);
    const longInput = specialSnapshot.elements.find((element) => element.tag === 'input' && element.name.startsWith('x'.repeat(79)));
    assert.equal(longInput.name, `${'x'.repeat(79)}�`, 'long names must compare using the same 80-unit snapshot value');
    const fillResult = await chrome.evaluate(`(async () => {
      try {
        return await FerroAdapter.perform({op:'fill', selector:'ferro-target:${encodeTarget(longInput)}', text:'test', origin:location.origin, deadlineMs:Date.now()+5000});
      } catch (error) { return {error:error.message}; }
    })()`);
    assert.deepEqual(fillResult, {}, 'an unchanged long-label target should remain usable');
    const astralButton = specialSnapshot.elements.find((element) => element.tag === 'button' && element.text?.startsWith('z'.repeat(79)));
    assert.ok(astralButton, 'astral text button should be present in the snapshot');
    const clickResult = await chrome.evaluate(`(async () => {
      try {
        return await FerroAdapter.perform({op:'click', selector:'ferro-target:${encodeTarget(astralButton)}', origin:location.origin, deadlineMs:Date.now()+5000});
      } catch (error) { return {error:error.message}; }
    })()`);
    assert.deepEqual(clickResult, {}, 'text truncated across an astral character should still match the same target');

    const target = encodeTarget(signIn);
    await chrome.evaluate(`(() => {
      const section = document.createElement('section');
      section.innerHTML = '<button>Inserted before the old target</button>';
      document.body.insertBefore(section, document.querySelector('section'));
    })()`);
    const staleResult = await chrome.evaluate(`(async () => {
      try {
        await FerroAdapter.perform({op:'fill', selector:'ferro-target:${target}', text:'x', origin:location.origin, deadlineMs:Date.now()+5000});
        return 'allowed';
      } catch (error) { return error.message; }
    })()`);
    assert.match(staleResult, /stale ref/, 'a DOM change must reject a path that now points at a different element');
  } finally {
    await chrome.close();
  }
});

test('execution snapshots pin input and form identity before trusted input', async (t) => {
  if (process.env.FERRO_TEST_BROWSER !== '1' || !findChrome()) {
    t.skip('requires FERRO_TEST_BROWSER=1 and Chrome');
    return;
  }
  const chrome = await launchChrome({windowSize: WINDOW_SIZE});
  try {
    await chrome.navigate('file://' + FIXTURE_PATH);
    await chrome.evaluate(fs.readFileSync(ADAPTER_PATH, 'utf8'));
    await chrome.evaluate(`(() => {
      globalThis.inputCalls = 0;
      globalThis.chrome = {runtime:{sendMessage:async()=>{globalThis.inputCalls++; return {};}}};
      const form = document.createElement('form');
      form.id = 'identity-form'; form.action = 'https://example.test/submit'; form.method = 'post';
      form.innerHTML = '<input id="identity-input" type="text" autocomplete="off" aria-label="Synthetic field"><button id="identity-button">Synthetic submit</button>';
      document.body.prepend(form);
    })()`);
    const snapshot = await chrome.evaluate(`FerroAdapter.takeSnapshot(${MAX_ELEMENTS}, true)`);
    const field = snapshot.elements.find((entry) => entry.selector === '#identity-input');
    const button = snapshot.elements.find((entry) => entry.selector === '#identity-button');
    assert.ok(field && button);
    assert.equal(field.input_type, 'text');
    assert.equal(field.autocomplete, 'off');
    assert.equal(button.form_action, 'https://example.test/submit');
    assert.equal(button.form_method, 'post');
    const target = (entry) => 'ferro-target:' + Buffer.from(JSON.stringify(entry)).toString('base64url');
    const link = snapshot.elements.find((entry) => entry.tag === 'a' && entry.abs_href);
    assert.ok(link);
    for (const href of ['https://other.example.test/target', link.abs_href + '?changed=1']) {
      await chrome.evaluate(`document.querySelector(${JSON.stringify(link.selector)}).href = ${JSON.stringify(href)}`);
      const result = await chrome.evaluate(`(async()=>{try {await FerroAdapter.perform({op:'click',selector:${JSON.stringify(target(link))},origin:location.origin,deadlineMs:Date.now()+5000});return 'allowed';}catch(error){return error.message;}})()`);
      assert.match(result, /execution identity changed/);
    }
    await chrome.evaluate(`document.querySelector('#identity-input').type = 'email'`);
    const fieldResult = await chrome.evaluate(`(async()=>{try {await FerroAdapter.perform({op:'fill',selector:${JSON.stringify(target(field))},text:'synthetic',origin:location.origin,deadlineMs:Date.now()+5000});return 'allowed';}catch(error){return error.message;}})()`);
    assert.match(fieldResult, /execution identity changed/);
    await chrome.evaluate(`document.querySelector('#identity-form').action = 'https://other.example.test/submit'`);
    const buttonResult = await chrome.evaluate(`(async()=>{try {await FerroAdapter.perform({op:'click',selector:${JSON.stringify(target(button))},origin:location.origin,deadlineMs:Date.now()+5000});return 'allowed';}catch(error){return error.message;}})()`);
    assert.match(buttonResult, /execution identity changed/);
    assert.equal(await chrome.evaluate('globalThis.inputCalls'), 0, 'changed identity must be refused before trusted input');
  } finally {
    await chrome.close();
  }
});
