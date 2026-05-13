# Embedded LaTeX example

Regular markdown prose appears as usual.

A `latex` fenced block renders through pandoc:

```latex
\section{From LaTeX}
A paragraph with \emph{emphasis} and a quick equation:
$$\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}$$
```

Markdown continues here. Inline math like $a^2 + b^2 = c^2$ also renders
via the same KaTeX path because the markdown passes `$..$` through
unchanged.

A `go` fenced block stays a code block:

```go
package main
import "fmt"
func main() { fmt.Println("hi") }
```
