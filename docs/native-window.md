# Native preview window: investigation

## Why

`mdp` currently spawns the user's browser to display the preview. The path is in `internal/config/config.go` (`autoBrowserCmd`): probe for Chromium-family first (`--app=URL` gives a chromeless single-window UX), fall back to Firefox-family (`--new-window`, regular tab), then `xdg-open` / `open`.

The fallback paths are the problem. Users without Chrome get a tabbed window, not a chromeless one. For the primary consumer (the Neovim plugin user who alt-tabs between editor and preview constantly), a tabbed window is materially worse, not cosmetic. The goal of this investigation: support more users with a chromeless preview, regardless of which browser they have.

## Constraints

- Go 1.26.2 codebase. Goreleaser ships static binaries for linux + darwin × amd64 + arm64 with `CGO_ENABLED=0`. Preserving this is highly desirable: it keeps the release pipeline simple, lets cross-compile work on any single build host, and produces binaries that run on minimal distros.
- The preview needs full HTML rendering, JavaScript execution, and WebSocket support (for the `reload` / `scroll` / `navigate` flow driven by the `internal/server` HTTP+WS server).
- Window-close detection is nice-to-have: it lets the server shut down cleanly instead of relying on the `wsScriptTemplate` fallback notice in `internal/render/page.go`.

## Options compared

### A. `webview/webview_go` (in-process, CGO)

Thin Go binding over the C `webview/webview` library. Uses WebKitGTK on Linux, WKWebView on macOS. Tiny API (`New`, `Navigate`, `Run`, `Bind`, `Terminate`). JS + WebSockets work natively. Window close stops `Run()`, so `defer server.Shutdown()` works.

- CGO required. `CGO_ENABLED=0` would no longer build.
- Cross-compile: requires `goreleaser-cross` Docker image with per-target C toolchains and sysroots. Asymmetric pain: darwin from linux via `osxcross` is documented and not terrible; linux/arm64 with WebKitGTK headers is the actual hard part.
- Linux runtime dep: `libwebkit2gtk-4.1` (or 4.0). Present on every desktop distro that ships GNOME apps. Absent on Alpine, minimal Arch, server images, headless CI.
- macOS: links against system `WebKit.framework`, no extra install needed.
- Binding itself is quieter (last commit 2024-08); the upstream C library is active.

### B. Wails

Full app framework. Same CGO + WebKit engines as A, but with extra ceremony. v3 exposes `Navigate(url)` so you could in theory shell it as "just a window," but you're paying the Wails toolchain cost on top.

- CGO required.
- **Cannot cross-compile between OSes.** Wails requires a native build host per target. Hard incompatibility with the current goreleaser matrix.
- ~10-15 MB empty binary. Heavy for a small CLI.

Strictly worse than A for this use case.

### C. Rust `wry` sidecar

Ship a tiny precompiled Rust binary (`mdp-webview`) alongside the Go binary in the goreleaser tarball. Go spawns it: `mdp-webview http://localhost:PORT`. The sidecar uses `wry` (Tauri's webview crate) to open the window with the OS engine. Go observes the sidecar's exit via `cmd.Wait()`.

- Go binary stays `CGO_ENABLED=0`. The release pipeline doesn't change.
- Sidecar size: ~3-5 MB stripped, per platform.
- Linux runtime dep: same `libwebkit2gtk-4.1`.
- macOS: zero extra deps (system WKWebView).
- Cost: a separate Rust release pipeline. Tauri itself documents that meaningful cross-compile isn't viable; the typical pattern is a GitHub Actions matrix per target, with goreleaser pulling the 4 prebuilt artifacts via `extra_files`.
- Two languages in the project.

### D. Per-OS native sidecars

Variant of C: instead of one Rust sidecar, ship platform-specific helpers.

- **Darwin: Swift WKWebView shim.** ~50 lines. `swift build` produces a standalone binary. WKWebView is system-provided, no extra runtime deps. Notarization is NOT required for binaries the user `chmod +x`'d themselves in `~/.local/bin`.
- **Linux: WebKitGTK shim (C or whatever).** Still drags the `libwebkit2gtk-4.1` runtime dep onto users. More codebases to maintain.

The darwin half is small and clean. The linux half is no better than C.

### E. Firefox `--kiosk`

`firefox --kiosk URL` still works in Firefox 2026, and is genuinely chromeless. But it's full-screen kiosk mode: it claims the whole monitor, intercepts shortcuts, and locks out window management. Wrong shape for a side-by-side preview. Usable only on a secondary monitor.

### F. Firefox + `userChrome.css` SSB hack

`firefox --new-instance --no-remote -P <profile> URL` with a custom `userChrome.css` hiding the tab bar and address bar. Requires `toolkit.legacyUserProfileCustomizations.stylesheets=true` in `about:config` and per-profile CSS files. Fragile: CSS selectors break with major Firefox UI revs (the v103 → v140 churn is documented). Per-user setup that mdp cannot do silently. Dead end.

### G. `gjs` sidecar on Linux

Go spawns `gjs preview.js http://localhost:PORT`. ~30 lines of GJS using `WebKit2.WebView` + `load_uri` + `close-request`. No CGO. GJS+WebKit2 is present by default on every GNOME desktop (it's how GNOME Shell extensions run), but absent on KDE base, Alpine, most minimal containers. Marginal value: the user population it helps is narrow.

### H. `purego` (no CGO, runtime FFI from Go)

[`purego`](https://github.com/ebitengine/purego) is pure-Go FFI from the Ebitengine team. It does what CGO does (call C / dynamic libraries) but at runtime via `dlopen` / `dlsym`. The Go binary compiles with `CGO_ENABLED=0`, and the linked libraries are resolved at startup. The runtime library still has to exist on the user's machine (no static linking), but the build pipeline stays clean: no cross-compile sysroots, no `goreleaser-cross`.

This unlocks a real hybrid (what actually shipped):

- **Darwin**: pure [`purego/objc`](https://github.com/ebitengine/purego). Calls Cocoa via `objc_msgSend` directly (NSApplication, NSWindow, WKWebView). No CGO, no darwinkit. The original plan was to use [`darwinkit`](https://github.com/progrium/darwinkit), but a closer look revealed darwinkit's `helper/action` and `dispatch` packages use `#cgo` directives, transitively forcing CGO. Dropped in favor of a ~165-line `nativewin_darwin.go` that uses `purego/objc` directly.
- **Linux**: handrolled purego bindings to `libwebkit2gtk` (tries `-4.1` first, falls back to `-4.0` for Ubuntu 22.04 LTS / Debian 11 / RHEL 9). ~150 lines of dlopen + `RegisterLibFunc` against GTK 3, GObject, and WebKit2GTK.

Build-constrained:

```
//go:build darwin   -> purego/objc + NSWindow/WKWebView, no CGO, no extra deps
//go:build linux    -> purego + WebKitGTK, no CGO, libwebkit2gtk-4.1 or -4.0 at runtime
```

Single Go binary per platform. `CGO_ENABLED=0` throughout. goreleaser's existing cross-compile pipeline works unchanged.

### Note on terminology

H ("purego") does NOT use the `webview/webview` C library. It calls the OS engines (`WKWebView`, `WebKitGTK`) directly. Functionally it's "a webview," just without the webview-the-C-library middleman. Same rendering, same JS+WebSocket support, one less abstraction layer.

## Decision matrix

| Option | CGO in Go binary | Helps non-Chrome users | Cross-compile preserved | Notes |
| --- | --- | --- | --- | --- |
| A. `webview_go` direct | Yes | Yes | No (needs `goreleaser-cross`) | One language, one binary |
| B. Wails | Yes | Yes | No (no cross-OS) | Heavier, no upside vs A |
| C. Rust `wry` sidecar | No | Yes | Yes | Two languages, two pipelines |
| D. Per-OS native sidecars | No | Yes | Yes | Darwin half is cheap; linux half is not |
| E. Firefox `--kiosk` | No | Yes (Firefox users) | Yes | Fullscreen-only, wrong shape |
| F. Firefox + userChrome.css | No | Yes (Firefox users) | Yes | Per-user setup, fragile |
| G. `gjs` Linux sidecar | No | Yes (GNOME users) | Yes | Narrow coverage |
| H. `purego` (purego/objc darwin + handrolled linux) | No | Yes | Yes | What shipped. Write linux + darwin bindings ourselves. |

## Eliminated

- **chromedp**, **lorca**: both require Chrome. Eliminated by the "support non-Chrome users" framing.
- **go-astilectron**: archived, bundles Electron.
- **`qlmanage`** (macOS Quick Look): no JS, no WebSockets.
- **`osascript` WebView trick**: doesn't exist; macOS 26 Tahoe broke a lot of osascript app-activation anyway.

## Trade-offs at the top

The interesting candidates are A, C, and H. They give the same final UX (native chromeless window everywhere, full JS+WebSocket). They differ in where you pay the cost:

- **A pays in the Go release pipeline** (CGO + goreleaser-cross + WebKitGTK sysroots for linux/arm64).
- **C pays in a second release pipeline** (Rust GitHub Actions matrix feeding goreleaser via `extra_files`).
- **H pays in code maintenance** (write both the linux purego/WebKitGTK bindings and the darwin purego/objc bindings yourself).

H is the only option that preserves both `CGO_ENABLED=0` AND single-language. Its cost is one-time engineering (a few days of writing dlopen bindings against the WebKitGTK C API) rather than ongoing pipeline complexity. Worst-case fallback if the linux bindings turn out fragile: temporarily wrap webview_go behind a `linux + cgo` build tag while the purego path stabilizes.

## Common runtime dep

A, C, D-linux, and H-linux all require `libwebkit2gtk-4.1` on the user's machine. This dep is unavoidable for any "real native window" story on Linux. Document it in `install.sh` and call it out in the README. Present on every desktop Linux that ships GNOME software; absent on Alpine, minimal Arch, server images, headless CI.

## What shipped

Option H, behind `MDP_NATIVE=1` env-var gate in `mdp watch`. Files:

- `internal/nativewin/nativewin.go` — public API (`Open(opts) error`, `Available() bool`, `ErrUnsupported`).
- `internal/nativewin/nativewin_linux.go` — purego + `libwebkit2gtk-4.1` (with `-4.0` fallback) + GTK 3.
- `internal/nativewin/nativewin_darwin.go` — purego/objc + NSWindow + WKWebView with autorelease pool, retained window delegate, and `postEvent:atStart:` after `[NSApp stop:]` for runloop unblock.
- `internal/nativewin/nativewin_other.go` — stub returning `ErrUnsupported` for non-linux/darwin.
- `cmd/mdp/main.go` — `Environment.OpenWindow` seam, `runWatchWithNativeWindow` helper.
- `cmd/mdp/main_darwin.go` — `runtime.LockOSThread()` in `init()` for the Cocoa main-thread requirement.

The browser-spawn path in `autoBrowserCmd` stays in place as the fallback for users without `libwebkit2gtk`, users who explicitly configure a browser, or users who don't set `MDP_NATIVE=1`.

## Open follow-ups

- Smoke-test the darwin backend on a real Mac (cross-builds clean, but has never been executed).
- Once stable, consider flipping the default so the native window is preferred when available and the browser is the fallback. Likely shape: replace the `MDP_NATIVE` env check with a `config.toml` `window = "native" | "browser" | "auto"` setting.
- Add a clean server shutdown handshake so window-close cleanly drains the HTTP server instead of relying on process exit.
