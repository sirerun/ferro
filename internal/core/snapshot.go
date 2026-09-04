package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/chromedp/chromedp"
)

// Element is one interactive item in a page snapshot. Refs are per-snapshot
// integers; the Executor maps them back to live CDP nodes.
type Element struct {
	Ref       int    `json:"ref"`
	Tag       string `json:"tag"`
	Role      string `json:"role,omitempty"` // e.g. "button", "textbox"
	Text      string `json:"text,omitempty"` // visible label, truncated
	Name      string `json:"name,omitempty"` // form control name/label/placeholder
	HREF      string `json:"href,omitempty"` // links only, path-only
	BackendID int    `json:"-"`              // CDP DOM node backend ID
}

// Snapshot is the compact page representation sent to the planner.
type Snapshot struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Elements  []Element `json:"elements"`
	Truncated bool      `json:"truncated,omitempty"` // element cap hit; planner should know
}

// Render produces the text form used in planner prompts. Kept terse:
// every token here costs money on every call.
func (s *Snapshot) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "url: %s\ntitle: %s\n", s.URL, s.Title)
	for _, e := range s.Elements {
		parts := []string{fmt.Sprintf("[%d]", e.Ref), e.Tag}
		if e.Role != "" && e.Role != e.Tag {
			parts = append(parts, e.Role)
		}
		if e.Name != "" {
			parts = append(parts, strconv.Quote(e.Name))
		}
		if e.Text != "" {
			parts = append(parts, strconv.Quote(e.Text))
		}
		if e.HREF != "" {
			parts = append(parts, e.HREF)
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

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
		Tag  string `json:"tag"`
		Role string `json:"role"`
		Name string `json:"name"`
		Text string `json:"text"`
		HREF string `json:"href"`
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
			Ref:  i + 1, // 1-based; 0 means "unset" for validation
			Tag:  r.Tag,
			Role: r.Role,
			Name: r.Name,
			Text: r.Text,
			HREF: r.HREF,
		})
	}
	return snap, nil
}

// RefByHint finds an element matching loose criteria — used by the repair
// path to remap a stale ref after a re-snapshot.
func (s *Snapshot) RefByHint(tag, text string) (int, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, e := range s.Elements {
		if e.Tag != tag {
			continue
		}
		if strings.Contains(strings.ToLower(e.Text), text) ||
			strings.Contains(strings.ToLower(e.Name), text) {
			return e.Ref, true
		}
	}
	return 0, false
}
