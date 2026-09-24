The glass-chat.css component is adapted from the Glass Chat design supplied
by the repository owner for Ferro. It uses the supplied bubble, fade and
composer styles. The standalone panel behavior is implemented locally;
HTMX, its demo backend and the template's third-party icon are not bundled.

Chrome owns the panel's outer frame and header. Ferro matches its content
background to the system light/dark frame approximation, with a local color
picker for custom Chrome themes. No page-wide overlay or animation loop is
used. Glass surfaces blur the panel's own background, not the website behind
Chrome's separate panel surface.

The Ferro brand mark is generated artwork: an arrow focus cursor with a
segmented cyan ring. The extension's Chrome icons use transparent PNG sizes;
the side panel and pairing popup use the SVG source. A low-energy pulse on the
side-panel mark can be disabled by reduced-motion preferences.
