---
title: Sample document
---

# Sample document

This document is the source-of-truth for the binary format fixtures
under `testdata/formats/`. Regenerate the siblings with:

```sh
pandoc fixture-source.md -o sample.docx
pandoc fixture-source.md -o sample.odt
pandoc fixture-source.md -o sample.epub
pandoc fixture-source.md -o sample.fb2
pandoc fixture-source.md -o sample.pptx
```

`sample.xlsx` is generated separately from `sample.xlsx-source.csv`
via `libreoffice --headless --convert-to xlsx`, since pandoc 3.x
cannot write xlsx (only read it).

## Features

- Headings, paragraphs, and **bold** / *italic* prose
- Lists with `inline code` and an [external link](https://example.com)
- Fenced code blocks
- Tables and blockquotes

## Code sample

```python
def greet(name):
    return f"Hello, {name}!"

print(greet("pandoc"))
```

## Comparison table

| Format | Kind   | Notes                          |
| ------ | ------ | ------------------------------ |
| docx   | binary | Office Open XML, zip-packed    |
| odt    | binary | OpenDocument Text              |
| epub   | binary | EPUB e-book                    |
| fb2    | text   | FictionBook XML                |
| pptx   | binary | PowerPoint, input only in 3.5+ |
| xlsx   | binary | Spreadsheet, input only        |

> Final line: a blockquote so the rendered output exercises every
> block-level construct exposed by the renderer.
