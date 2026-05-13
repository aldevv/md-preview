# Links demo

Click each entry below to see how mdp handles every link variant.
The first four navigate cleanly; the rest pop a toast and leave the
page unchanged.

## Pre-rendered siblings

- [Sibling page (`sibling.md`)](sibling.md)
- [Nested page (`nested/deep.md`)](nested/deep.md)

## Renderable, but no traversal needed

- [SVG asset (`logo.svg`)](logo.svg) (browser handles; not a
  markdown document but still in-tree)

## Errors (toast, no navigation)

- [Missing file (`gone.md`)](gone.md)
- [LaTeX doc (`paper.tex`)](paper.tex) — non-md formats need
  `mdp watch` for click-to-render
- [Out of tree (`../escape.md`)](../escape.md)

## External

- [External link](https://example.com) — opens in the same tab,
  browser-native (no interception)
- [Page anchor (`#external`)](#external) — scrolls within the
  current document
