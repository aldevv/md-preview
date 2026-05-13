# Markdown with embedded Mermaid diagrams

Mdp recognizes ` ```mermaid ` fences and emits a `<pre class="mermaid">`
wrapping the raw source. The embedded mermaid.js bundle (loaded
conditionally, only when a mermaid fence is present) auto-runs on
page load and replaces each block with an inline SVG.

## Flowchart

```mermaid
flowchart TD
  A[Start] --> B{Has pandoc?}
  B -->|Yes| C[Render fence]
  B -->|No| D[Auto-fetch pandoc]
  D --> C
  C --> E[Done]
```

## Sequence diagram

```mermaid
sequenceDiagram
  participant Editor
  participant mdp
  participant Browser
  Editor->>mdp: stdin render
  mdp->>Browser: WS reload v=42
  Browser->>mdp: GET /
  mdp-->>Browser: rendered HTML
```

## Class diagram

```mermaid
classDiagram
  class PandocRenderer {
    +Render(ctx, src, format) string
    +Available() bool
  }
  class GoldmarkRenderer {
    +RenderBytes(content) string
  }
  PandocRenderer <|-- GoldmarkRenderer : fence intercept
```

Prose after the diagrams continues normally.
