# Markdown with embedded LaTeX

Prose surrounding a fenced ` ```latex ` block. The renderer detects
the `latex` info string, shells out to pandoc with `--from=latex`,
sanitizes the HTML, and substitutes a `<div class="pandoc-block">` in
place of the fence. Surrounding markdown continues to render
normally.

## Sectioning and math

```latex
\section{Embedded section}

The quadratic formula:
\[
  x = \frac{-b \pm \sqrt{b^2 - 4ac}}{2a}
\]

A bullet list:
\begin{itemize}
  \item first item
  \item second item
\end{itemize}
```

After the fenced block, prose continues. The `data-line` attribute on
the rendered `<div>` keeps the editor scroll-sync attribution intact
at block granularity.

## Inline tex info aliases

The `tex` and `pandoc-latex` info strings route through the same path:

```tex
\textbf{bold from a tex-tagged fence}
```
