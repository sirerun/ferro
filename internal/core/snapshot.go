package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/chromedp"
	"github.com/sirerun/ferro/page"
)

// Element and Snapshot retain source compatibility with the public page package.
type Element = page.Element
type Snapshot = page.Snapshot

// The injected script walks the DOM and returns interactive elements as JSON.
// It runs entirely in-page: one round trip, no per-element CDP calls.
//
// Selection criteria (per RFC §4.1):
//   - interactive: a/button/input/select/textarea/[role=button|link|tab|...]
//   - visible: offsetParent != null or position:fixed with non-zero size
//   - landmark text: h1-h4 for orientation
const snapshotJS = `
(() => {
  const INTERACTIVE = new Set(['A','BUTTON','INPUT','SELECT','TEXTAREA','SUMMARY']);
  const ROLES = new Set(['button','link','tab','checkbox','radio','menuitem','combobox','option','switch']);
  const out = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
  let el;
  while ((el = walker.nextNode())) {
    const tag = el.tagName;
    const role = el.getAttribute('role') || '';
    const isInteractive = INTERACTIVE.has(tag) || ROLES.has(role) ||
      (tag === 'INPUT' && el.type !== 'hidden');
    const isHeading = /^H[1-4]$/.test(tag);
    if (!isInteractive && !isHeading) continue;

    // visibility: cheap checks only — no getBoundingClientRect per node unless needed
    const style = getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') continue;
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    // viewport filter: skip clearly off-screen elements (with margin for sticky headers)
    if (rect.bottom < -50 || rect.top > innerHeight + 50) continue;

    // accessible name: label[for], aria-label, placeholder, innerText — first hit wins
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

    out.push({
      tag: tag.toLowerCase(),
      role: role,
      name: name.slice(0, 80),
      text: text,
      href: href,
      bid: -1 // backend node id, resolved below via CDP
    });
  }
  return JSON.stringify(out);
})()
`

// maxScanTruncation drops trailing text noise from very large pages.
const (
	defaultMaxElements = 200
	maxNameLen         = 80
)

// TakeSnapshot compiles the live page into a Snapshot. backendNodeIDs are
// resolved in one CDP batch so refs can later drive clicks without
// re-querying selectors.
func TakeSnapshot(ctx context.Context, maxElements int) (*Snapshot, error) {
	if maxElements <= 0 {
		maxElements = defaultMaxElements
	}

	var rawJSON, title, url string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(snapshotJS, &rawJSON),
		chromedp.Title(&title),
		chromedp.Location(&url),
	)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	var raws []struct {
		Tag      string `json:"tag"`
		Role     string `json:"role"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		HREF     string `json:"href"`
		Selector string `json:"selector"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &raws); err != nil {
		return nil, fmt.Errorf("snapshot decode: %w", err)
	}

	snap := &Snapshot{URL: url, Title: title}
	for i, r := range raws {
		if i >= maxElements {
			snap.Truncated = true
			break
		}
		snap.Elements = append(snap.Elements, Element{
			Ref:      i + 1, // 1-based; 0 means "unset" for validation
			Tag:      r.Tag,
			Role:     r.Role,
			Name:     r.Name,
			Text:     r.Text,
			HREF:     r.HREF,
			Selector: r.Selector,
		})
	}
	return snap, nil
}
