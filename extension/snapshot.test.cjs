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
  } finally {
    await chrome.close();
  }
});
