---
title: Everything-and-the-kitchen-sink fixture
author: Alejandro Bernal
date: 2026-05-13
tags:
  - markdown
  - kitchen-sink
---

# Full feature exercise

This fixture combines every renderer feature into one file:
frontmatter, GFM extensions, embedded LaTeX via fenced blocks,
KaTeX math (display + inline), and ordinary code blocks.

## Prose, links, emphasis

A paragraph with **bold**, *italic*, ***both***, `inline code`, and
an [external link](https://example.com). Autolinks like
https://github.com/aldevv/md-preview should also activate.

## Table

| Feature        | Backed by                |
| -------------- | ------------------------ |
| Markdown       | goldmark + GFM extension |
| LaTeX fences   | host pandoc subprocess   |
| Math rendering | embedded KaTeX           |
| Scroll-sync    | data-line + WS protocol  |

## Task list

- [x] Add frontmatter
- [x] Embed a fenced LaTeX block
- [x] Include block + inline math
- [ ] Implement teleportation

## Alerts

> [!NOTE]
> Alerts use `> [!KIND]` and are pre-styled by mdp's CSS.

> [!CAUTION]
> Mixing too many features into one fixture risks an unreadable
> rendered page; that's the point of this one.

## Inline + block math

The classic identity is \(e^{i\pi} + 1 = 0\). And as a block:

$$
\sum_{k=0}^{n} \binom{n}{k} = 2^n
$$

## Embedded LaTeX

Pandoc renders the fence body and injects it inline:

```latex
\section*{Embedded \LaTeX{}}

\begin{itemize}
  \item Rendered via host pandoc, not goldmark
  \item Wrapped in \verb|<div class="pandoc-block">|
  \item Inline math like $x^2$ inside the fence falls through to MathJax markers
\end{itemize}
```

## Code block (non-LaTeX)

```python
def fibonacci(n):
    a, b = 0, 1
    for _ in range(n):
        yield a
        a, b = b, a + b
```

## Blockquote

> A final blockquote so the rendered output exercises every block
> kind: paragraph, heading, list, task list, table, code block,
> fenced LaTeX, math (block and inline), GitHub alert, and this
> plain blockquote.
