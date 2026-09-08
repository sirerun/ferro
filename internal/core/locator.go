package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// resolveRef relocates a snapshot element by signature (tag + name/text),
// cache-first: if a resolution cache is attached and holds a selector for
// this element's signature that still matches exactly one visible element,
// that selector is used directly — zero LLM involvement, and no dependency
// on buildSelector's guess. Otherwise it falls back to building a selector
// from the snapshot element, as in the base v0.1 path.
//
// Refs are positional, so they can't survive to click-time directly; the
// signature is stable enough for typical pages, and failure routes to the
// repairer — which is exactly the designed path for drift.
func (x *Executor) resolveRef(ctx context.Context, ref int, kind string) (selector string, key CacheKey, err error) {
	snap, ok := snapshotFromCtx(ctx)
	if !ok {
		return "", CacheKey{}, fmt.Errorf("no snapshot in context; runner must attach one before execute")
	}
	el := snap.element(ref)
	if el == nil {
		return "", CacheKey{}, fmt.Errorf("ref %d not in snapshot", ref)
	}

	key = CacheKey{
		Host:      hostOf(snap.URL),
		Kind:      kind,
		Signature: el.Signature(),
	}

	// Fast path: cached selector for this exact element signature.
	if x.cache != nil {
		if sel, hit := x.cache.Get(key); hit {
			if selectorMatches(ctx, sel, el) {
				if x.metrics != nil {
					x.metrics.CacheHits++
				}
				return sel, key, nil
			}
			// Signature matched but selector didn't — site changed under
			// us. Don't invalidate yet; fall through and refresh on
			// success via recordOutcome.
		}
	}

	return buildSelector(*el), key, nil
}

// recordOutcome wires the cache to the error taxonomy from repair.go:
// successes build trust; stale refs invalidate. Timeouts do NOT invalidate
// (the selector may be fine; the page was just slow). A nil cache makes
// this a no-op.
func (x *Executor) recordOutcome(key CacheKey, sel string, err error) {
	if x.cache == nil || key.Signature == "" {
		return
	}
	if err == nil {
		x.cache.Put(key, sel)
		return
	}
	if Classify(&RunError{Action: Action{Kind: ActionKind(key.Kind)}, Err: err}) == ErrStaleRef {
		x.cache.Invalidate(key)
	}
}

// selectorMatches verifies the selector resolves to exactly one visible
// element, and that the element still resembles the signature (by name/text
// similarity). One round trip, pure JS.
func selectorMatches(ctx context.Context, sel string, e *Element) bool {
	want := normalizeText(e.Name + " " + e.Text)
	js := fmt.Sprintf(`(() => {
		const els = document.querySelectorAll(%q);
		if (els.length !== 1) return 'count:' + els.length;
		const el = els[0];
		const r = el.getBoundingClientRect();
		if (r.width === 0 && r.height === 0) return 'hidden';
		return (el.getAttribute('aria-label')||'') + ' ' + (el.innerText||'');
	})()`, sel)
	var got string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &got)); err != nil {
		return false
	}
	gotNorm := normalizeText(got)
	return gotNorm == want || (want != "" && strings.Contains(gotNorm, firstField(want)))
}

// firstField returns the first whitespace-separated field of s, or s itself
// if it has none — used for a loose partial-match check in selectorMatches.
func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	return fields[0]
}

// buildSelector constructs a CSS selector from an element's signature,
// most-specific-first.
//
// Known limitation: signature-based selectors (button[aria-label="..."]) can
// match multiple nodes or drift on dynamic text. Mitigations, in order:
// (1) the first match is used — acceptable for v0.1; (2) failures route to
// the repairer with a fresh snapshot; (3) the M4 resolution cache stores
// what actually worked, keyed on signature, so repeat runs skip this
// fragility entirely. This is the known weakest joint in v0.1 and it's
// deliberately routed through the repair path rather than over-engineered
// now (see repair.go and the RFC discussion).
func buildSelector(e Element) string {
	if e.HREF != "" && e.Tag == "a" {
		return fmt.Sprintf(`a[href^="%s"]`, cssEscape(e.HREF))
	}
	// Prefer aria-label / placeholder / name (stable, semantic). The
	// snapshot compiler collapses whichever of these matched first into
	// Element.Name without recording which one — so the selector must try
	// all three as a CSS group (first match in document order wins) rather
	// than guessing a single attribute.
	if e.Name != "" {
		var parts []string
		for _, attr := range []string{"aria-label", "placeholder", "name"} {
			parts = append(parts, fmt.Sprintf(`%s[%s="%s"]`, e.Tag, attr, cssEscape(e.Name)))
		}
		return strings.Join(parts, ", ")
	}
	// Fall back to the bare tag — the caller (doClick/doFill/...) will hit
	// the first match; a wrong-element hit routes to the repairer.
	return e.Tag
}

func cssEscape(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range s {
		if c == '"' || c == '\\' {
			out = append(out, '\\')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

type snapshotKey struct{}

func withSnapshot(ctx context.Context, s *Snapshot) context.Context {
	return context.WithValue(ctx, snapshotKey{}, s)
}

func snapshotFromCtx(ctx context.Context) (*Snapshot, bool) {
	s, ok := ctx.Value(snapshotKey{}).(*Snapshot)
	return s, ok
}
