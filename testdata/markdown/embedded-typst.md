# Markdown with embedded Typst

Mdp's fenced-pandoc dispatch recognizes ` ```typst ` (and the
shorter ` ```typ `) info strings, routes the body through host
pandoc with `--from=typst`, and wraps the result in a
`<div class="pandoc-block">`.

```typst
= Hello from Typst

This paragraph has *bold* and _italic_ markup. Typst uses a syntax
distinct from both Markdown and LaTeX.

- list item one
- list item two
- list item three

$ pi approx 3.14159 $
```

Prose after the fence resumes normal markdown rendering.
