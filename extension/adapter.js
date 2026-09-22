/* extension/adapter.js -- generic DOM-execution adapter (T12.2, ADR 006).
 *
 * Unlike ~/Code/dndungu/ox/extension/adapter.js (hardcoded to oxalpha.com's
 * CSS classes), this adapter executes whatever action the bridge
 * (internal/extbridge, T12.1) hands it against the live DOM of whichever
 * site the paired tab happens to be showing. There is no fixed target site.
 *
 * Ported from ox, generalized:
 *   - takeSnapshot(): ox has no equivalent (it never sends a page inventory
 *     to the runner). This is a JavaScript port of
 *     internal/core/snapshot.go's element-selection and numbering
 *     algorithm -- see that file's `snapshotJS` constant, which this
 *     function's DOM-walk half is copied from verbatim (same INTERACTIVE
 *     tag set, same ROLES set, same visibility/viewport filters, same
 *     name/text extraction order, same truncation rules), plus the Go
 *     side's ref-numbering (1-based, in document order) and maxElements
 *     truncation, so "ref N" means the identical element regardless of
 *     which backend produced the snapshot. THIS IS THE SINGLE MOST
 *     SAFETY-CRITICAL PIECE OF THIS EXTENSION -- see
 *     internal/core/snapshot_parity_test.go and
 *     extension/snapshot.test.cjs, which assert this function's
 *     output against snapshot.go's for a shared fixture
 *     (extension/testdata/fixture.html). Any change here MUST be mirrored
 *     in snapshot.go (and vice versa) and re-verified by both tests --
 *     this is a standing rule (docs/plan.md's Operating Procedure), not a
 *     one-time port.
 *   - blocked(): generalized from ox's oxalpha-specific text matching
 *     (ADR 006 decision #5) into generic, site-agnostic structural/text
 *     heuristics: a visible password/email input (login gate), a Cloudflare
 *     or other known challenge-provider iframe, and a generic verification/
 *     rate-limit vocabulary scanned from ARIA alert/status regions and
 *     class/id hints (not any one site's copy).
 *   - perform(): ox's perform() is a hardcoded state machine over one
 *     site's chat UI (new/send/copy/snapshot). This perform() is a generic
 *     dispatcher over ferro's core.Action vocabulary (navigate is handled
 *     by background.js instead -- see its header comment -- everything
 *     else lands here): click, fill, select, key, scroll, wait_visible,
 *     extract, snapshot.
 *   - Trusted click/key: delegated to background.js's chrome.debugger
 *     technique (content scripts cannot use chrome.debugger themselves),
 *     ported from ox's retry-on-dropped-debugger logic. See background.js.
 *   - Fill: native property setter + a dispatched `input` event (ADR 006
 *     decision #3; ox's exact technique) -- deliberately NOT the CDP
 *     keystroke-simulation ChromedpDriver's doFill uses, because text
 *     entry does not need trusted-event status the way clicks do.
 *
 * Deliberately NOT ported from ox: its clipboard-based "verified copy"
 * reply-fetching (oxalpha.com-specific reply reconstruction from markdown),
 * its `.msg-user`/`.msg-assistant`/`.new-chat-btn` selectors, and its
 * visual "connected" overlay dot/label (a nice-to-have, not part of the
 * documented action vocabulary; can be added later without touching the
 * execution logic below).
 */
(() => {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  // visible() is a coarse, cheap liveness check used only by blocked() and
  // wait_visible -- distinct from takeSnapshot()'s own (stricter, ordering-
  // sensitive) visibility filter below, exactly as ox's adapter.js also
  // keeps two separate visibility notions for two separate purposes.
  const visible = (el) =>
    !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';

  // ---------------------------------------------------------------------
  // takeSnapshot: faithful port of internal/core/snapshot.go's snapshotJS
  // (the DOM walk) plus TakeSnapshot's Go-side ref-numbering/truncation.
  // ---------------------------------------------------------------------
  function takeSnapshot(maxElements) {
    if (!maxElements || maxElements <= 0) maxElements = 200;

    // --- verbatim port of snapshot.go's `snapshotJS` DOM walk ---
    const INTERACTIVE = new Set(['A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA', 'SUMMARY']);
    const ROLES = new Set([
      'button', 'link', 'tab', 'checkbox', 'radio', 'menuitem', 'combobox', 'option', 'switch',
    ]);
    const raw = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
    let el;
    while ((el = walker.nextNode())) {
      const tag = el.tagName;
      const role = el.getAttribute('role') || '';
      const isInteractive = INTERACTIVE.has(tag) || ROLES.has(role) ||
        (tag === 'INPUT' && el.type !== 'hidden');
      const isHeading = /^H[1-4]$/.test(tag);
      if (!isInteractive && !isHeading) continue;

      // visibility: cheap checks only -- no getBoundingClientRect per node
      // unless needed (mirrors snapshot.go's comment verbatim).
      const style = getComputedStyle(el);
      if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') continue;
      const rect = el.getBoundingClientRect();
      if (rect.width === 0 && rect.height === 0) continue;
      // viewport filter: skip clearly off-screen elements (with margin for
      // sticky headers).
      if (rect.bottom < -50 || rect.top > innerHeight + 50) continue;

      // accessible name: label[for], aria-label, placeholder, innerText --
      // first hit wins.
      let name = el.getAttribute('aria-label') || el.getAttribute('placeholder') || '';
      if (!name && (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA')) {
        if (el.id) {
          const lbl = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
          if (lbl) name = lbl.innerText.trim();
        }
      }
      let text = isHeading ? el.innerText : (el.innerText || '').trim();
      if (text.length > 80) text = text.slice(0, 80) + '…';
      if (!name && !text && !el.getAttribute('aria-labelledby')) continue; // unlabeled, skip

      let href = '';
      if (tag === 'A' && el.getAttribute('href')) {
        href = new URL(el.getAttribute('href'), location.href).pathname;
      }

      raw.push({
        tag: tag.toLowerCase(),
        role: role,
        name: name.slice(0, 80),
        text: text,
        href: href,
      });
    }
    // --- end verbatim port ---

    // --- port of TakeSnapshot's Go-side numbering/truncation ---
    const elements = [];
    let truncated = false;
    for (let i = 0; i < raw.length; i++) {
      if (i >= maxElements) {
        truncated = true;
        break;
      }
      const r = raw[i];
      const out = { ref: i + 1, tag: r.tag };
      if (r.role) out.role = r.role;
      if (r.name) out.name = r.name;
      if (r.text) out.text = r.text;
      if (r.href) out.href = r.href;
      elements.push(out);
    }
    const snap = { url: location.href, title: document.title, elements: elements };
    if (truncated) snap.truncated = true;
    return snap;
  }

  // ---------------------------------------------------------------------
  // blocked(): generalized CAPTCHA/human-verification/login-gate detection
  // (ADR 006 decision #5). No site-specific copy -- structural + generic
  // vocabulary only.
  // ---------------------------------------------------------------------
  const CHALLENGE_IFRAME_SELECTOR = [
    'iframe[src*="challenges.cloudflare.com"]',
    'iframe[src*="hcaptcha.com"]',
    'iframe[src*="recaptcha"]',
    'iframe[src*="turnstile"]',
    'iframe[title*="challenge" i]',
    'iframe[title*="verification" i]',
  ].join(', ');

  const GENERIC_VERIFICATION_PATTERNS = [
    /verify\s+(that\s+)?you.?re\s+(a\s+)?human/,
    /confirm\s+you.?re\s+human/,
    /human\s+verification/,
    /checking\s+your\s+browser/,
    /unusual\s+traffic/,
    /automated\s+requests?/,
    /too\s+many\s+requests/,
    /access\s+denied/,
    /rate.?limited/,
    /are\s+you\s+a\s+robot/,
    /prove\s+you.?re\s+not\s+a\s+robot/,
    /waiting\s+for\s+verification/,
  ];

  function blocked() {
    if (!document.body) return '';

    const challengeFrame = Array.from(document.querySelectorAll(CHALLENGE_IFRAME_SELECTOR)).find(visible);
    if (challengeFrame) {
      return 'A verification/challenge iframe is visible on this tab. Complete it manually, then resume.';
    }

    // Generic human-verification / rate-limit banners: scan ARIA alert and
    // status regions plus generic captcha/challenge class or id hooks
    // first (narrow, low false-positive-rate), falling back to the full
    // body text (mirrors ox's own outside-container scan) so a banner
    // rendered without any ARIA hook is still caught.
    const hinted = Array.from(document.querySelectorAll(
      '[role="alert"], [role="status"], [aria-live], [class*="captcha" i], [id*="captcha" i], [class*="challenge" i], [id*="challenge" i]'
    )).filter(visible).map((e) => e.textContent).join(' ');
    const haystack = (hinted + ' ' + document.body.innerText).toLowerCase();
    if (GENERIC_VERIFICATION_PATTERNS.some((re) => re.test(haystack))) {
      return 'The page appears to require human/CAPTCHA verification or has rate-limited this session. Complete it manually, then resume.';
    }

    // A visible password or email input, with no action of ours having
    // asked for one, implies a login gate is blocking the paired tab.
    if (Array.from(document.querySelectorAll('input[type="password"]')).some(visible)) {
      return 'A sign-in form is visible on this tab. Sign in manually, then resume.';
    }

    return '';
  }

  // ---------------------------------------------------------------------
  // Action execution.
  // ---------------------------------------------------------------------
  function mustFind(selector) {
    let el;
    try {
      el = document.querySelector(selector);
    } catch (error) {
      throw new Error(`invalid selector ${JSON.stringify(selector)}: ${error.message}`);
    }
    if (!el) throw new Error(`no element matches ${JSON.stringify(selector)}`);
    if (document.querySelectorAll(selector).length !== 1) throw new Error('ambiguous selector; refusing to choose an arbitrary element');
    return el;
  }

  async function scrollIntoViewIfNeeded(el) {
    el.scrollIntoView({ block: 'center', behavior: 'instant' });
    await sleep(50);
  }

  // Native property setter + dispatched input/change event (ADR 006
  // decision #3; ox's exact fill technique) -- bypasses framework-attached
  // setters that would otherwise swallow a plain `el.value = ...` write.
  function setNativeValue(el, value) {
    const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const descriptor = Object.getOwnPropertyDescriptor(proto, 'value');
    if (descriptor && descriptor.set) {
      descriptor.set.call(el, value);
      return;
    }
    el.value = value;
  }

  function fill(el, text) {
    el.focus();
    if ('value' in el) {
      setNativeValue(el, text);
      el.dispatchEvent(new Event('input', { bubbles: true }));
      el.dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }
    if (el.isContentEditable) {
      el.focus();
      el.textContent = text;
      el.dispatchEvent(new Event('input', { bubbles: true }));
      return;
    }
    throw new Error('element is not fillable (no value property, not contenteditable)');
  }

  // Mirrors internal/core/executor.go's doSelect JS exactly: match by
  // visible label first, fall back to value.
  function selectOption(el, value) {
    if (el.tagName !== 'SELECT') throw new Error('select target is not a <select> element');
    for (const opt of el.options) {
      if (opt.text.trim() === value || opt.value === value) {
        el.value = opt.value;
        el.dispatchEvent(new Event('change', { bubbles: true }));
        return;
      }
    }
    throw new Error(`select ${JSON.stringify(value)}: no matching option`);
  }

  async function extract(fields) {
    if (fields && Object.keys(fields).length > 0) {
      const keys = Object.keys(fields).sort(); // deterministic order, mirrors doExtract's sort.Strings
      const out = {};
      const failures = {};
      for (const field of keys) {
        try {
          const el = document.querySelector(fields[field]);
          if (!el) throw new Error('no matching element');
          out[field] = String(el.value ?? el.innerText ?? '').trim();
        } catch (error) {
          out[field] = '';
          failures[field] = error.message;
        }
      }
      if (Object.keys(failures).length === keys.length) {
        throw new Error(`extract: all ${keys.length} fields failed: ${JSON.stringify(failures)}`);
      }
      return out;
    }
    // No per-field selectors: hand back raw page text, mirroring
    // doExtract's schema-only fallback (structuring it against a schema is
    // the Runner's job, not the driver's).
    return document.body.innerText.slice(0, 20000);
  }

  async function waitVisible(selector, budgetMs) {
    const deadline = Date.now() + (budgetMs || 5000);
    for (;;) {
      const el = document.querySelector(selector);
      if (el && visible(el)) return;
      if (Date.now() >= deadline) throw new Error(`timed out waiting for ${JSON.stringify(selector)} to be visible`);
      await sleep(100);
    }
  }

  async function settle(budgetMs) {
    await new Promise((resolve) => {
      let quiet;
      const finish = () => { clearTimeout(quiet); clearTimeout(limit); observer.disconnect(); resolve(); };
      const observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(finish, 250); });
      const limit = setTimeout(finish, Math.max(1, budgetMs || 5000));
      quiet = setTimeout(finish, 250);
      observer.observe(document, {subtree:true, childList:true, attributes:true, characterData:true});
    });
  }

  async function trustedInput(kind, payload) {
    const response = await chrome.runtime.sendMessage({ type: 'ferro-trusted-input', kind, ...payload });
    if (response && response.error) throw new Error(response.error);
    return response;
  }

  // perform() is the dispatcher content.js calls for every non-navigate
  // action ("navigate" is handled directly by background.js -- see its
  // header comment for why). action shape: {op, selector, text, value,
  // to, fields, maxElements, budgetMs} -- a superset covering every
  // ferro core.Action kind this extension can execute directly against a
  // resolved CSS selector (ChromedpDriver's callers already resolve
  // ref -> selector Go-side before reaching the driver interface).
  async function perform(action) {
    const guard = () => {
      if (action.origin !== location.origin) throw new Error('origin changed before action');
      if (Date.now() >= action.deadlineMs) throw new Error('action expired before execution');
    };
    guard();
    // snapshot always runs, blocked or not -- it's how a caller (or a human
    // resuming later) sees what the block actually looks like. Every other
    // action short-circuits on a blocked page instead of acting into it.
    if (action.op === 'snapshot') {
      const snapshot = takeSnapshot(action.maxElements);
      const reason = blocked();
      return reason ? { blocked: reason, code: reason.startsWith('A sign-in') ? 'login_required' : 'blocked' } : { snapshot };
    }
    const reason = blocked();
    if (reason) return { blocked: reason, code: reason.startsWith('A sign-in') ? 'login_required' : 'blocked' };

    switch (action.op) {
      case 'click': {
        const el = mustFind(action.selector);
        await scrollIntoViewIfNeeded(el);
        guard();
        const rect = el.getBoundingClientRect();
        await trustedInput('click', { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, commandID:action.commandID });
        return {};
      }
      case 'fill': {
        const el = mustFind(action.selector);
        await scrollIntoViewIfNeeded(el);
        guard();
        fill(el, action.text || '');
        return {};
      }
      case 'select': {
        const el = mustFind(action.selector);
        selectOption(el, action.value);
        return {};
      }
      case 'key': {
        await trustedInput('key', { text: action.text || '', commandID:action.commandID });
        return {};
      }
      case 'scroll': {
        if (action.to === 'top') window.scrollTo(0, 0);
        else if (action.to === 'bottom') window.scrollTo(0, document.body.scrollHeight);
        else throw new Error(`scroll to ${JSON.stringify(action.to)}: ref-scroll not wired; use top|bottom`);
        return {};
      }
      case 'settle': {
        await settle(action.budgetMs);
        return {};
      }
      case 'wait_visible': {
        await waitVisible(action.selector, action.budgetMs);
        return {};
      }
      case 'extract': {
        const result = await extract(action.fields);
        return { result };
      }
      default:
        throw new Error(`unknown action op ${JSON.stringify(action.op)}`);
    }
  }

  globalThis.FerroAdapter = { perform, takeSnapshot, blocked };
})();
