# Alternative implementation: `webview_go`

This doc captures the path NOT taken in the native-window investigation (`docs/native-window.md`). Useful if the `purego` + `darwinkit` path runs into trouble and we need to switch.

## What it is

[`webview/webview_go`](https://github.com/webview/webview_go) is the canonical Go binding over the C `webview/webview` library. It picks up WebKitGTK on Linux, WKWebView on macOS, WebView2 on Windows. The Go-side API is tiny:

```go
import webview "github.com/webview/webview_go"

func main() {
    w := webview.New(false /* debug */)
    defer w.Destroy()
    w.SetTitle("mdp")
    w.SetSize(1024, 768, webview.HintNone)
    w.Navigate("http://localhost:43251/")
    w.Run() // blocks until window closes
}
```

## What it would look like in this codebase

- One import. No FFI to maintain. The library does the dlopen / Cocoa shim for you.
- The `internal/nativewin` package collapses to one platform-agnostic file (no `_linux.go` / `_darwin.go` split).
- `Open(opts) error` becomes ~10 lines.
- Window-close detection: returns from `Run()`. Trivial.

## What it would cost

### 1. CGO becomes mandatory

The current build is `CGO_ENABLED=0`. Switching `webview_go` in flips that to `CGO_ENABLED=1`. The Go binary now links against system C libraries at compile time.

### 2. Goreleaser rebuild

The current `.goreleaser.yaml` cross-compiles linux + darwin × amd64 + arm64 from any host. With CGO, that no longer works on a single machine. The standard fix is `goreleaser-cross` (a Docker image with pre-baked toolchains):

- `linux/amd64` to `linux/arm64`: needs `aarch64-linux-gnu-gcc` + arm64 WebKitGTK + GTK sysroot.
- `linux` to `darwin/amd64`: needs `osxcross` + macOS SDK.
- `linux` to `darwin/arm64`: same osxcross with arm64 target.

Goreleaser publishes a [cookbook entry for this](https://goreleaser.com/cookbooks/cgo-and-crosscompiling/) and provides the [`goreleaser-cross` Docker image](https://github.com/goreleaser/goreleaser-cross). Setup is ~1-2 days the first time.

### 3. Linux runtime dep

User's machine needs `libwebkit2gtk-4.1` installed (4.0 also works depending on which the library version is built against). Same dep as the `purego` path, just enforced at compile-link time rather than discovered at runtime.

### 4. Flexibility ceiling

The library's API is tiny: `New`, `Navigate`, `Run`, `Bind`, `Eval`, `Init`, `Dispatch`, `SetTitle`, `SetSize`, `Destroy`. Anything outside this surface (system tray, multiple windows, native menus, custom title bars, `WKWebView.createPDF`, etc.) requires either forking the C library or adding a second CGO library next to it. At which point you're paying the CGO cost for two libraries.

For mdp's current use case ("render HTML, push WebSocket messages, observe close") the ceiling is far above us and irrelevant.

### 5. Library health

`webview_go` itself was last touched 2024-08; the upstream C library `webview/webview` is actively maintained (last push 2026-03). Quiet binding, active engine. Not abandoned, but slower-moving than `purego` / `darwinkit`.

## When to switch back to this path

Pick `webview_go` over `purego` + `darwinkit` if:

- The handrolled Linux purego/WebKitGTK bindings turn out to be too fragile (crashes, subtle GTK threading issues, signal-callback memory issues) and the maintenance cost exceeds "rebuild the release pipeline once."
- The release pipeline is going to need `goreleaser-cross` for other reasons anyway (e.g., another CGO dep gets added).
- You're going to ship Windows builds, where `purego` is less polished and `webview_go` does WebView2 cleanly.
- You explicitly decide that staying single-language and accepting CGO is worth more than preserving `CGO_ENABLED=0`.

## How to switch

1. `go get github.com/webview/webview_go`.
2. Replace `internal/nativewin/nativewin_linux.go` and `nativewin_darwin.go` with one file using `webview_go`.
3. Update `.goreleaser.yaml` to set `env: [CGO_ENABLED=1]` and switch the build matrix to use `goreleaser-cross` Docker.
4. Update `install.sh` and the README to document the `libwebkit2gtk-4.1` runtime dep on Linux.
5. Delete the `purego` and `darwinkit` dependencies from `go.mod`.

The `internal/nativewin` public API (`Open(opts) error`, `Available() bool`) stays the same, so nothing in `cmd/mdp` needs to change.
