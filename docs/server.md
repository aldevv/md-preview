# Server mode

`mdp serve <file> <port> <theme>` runs a long-running HTTP+WebSocket server bound to `127.0.0.1:<port>`. It reads JSON commands on stdin (`render`, `scroll`, `quit`) and broadcasts reload/scroll messages over WebSockets to the connected browser tab. The [md-preview.nvim](https://github.com/aldevv/md-preview.nvim) plugin spawns this and communicates via stdin.

## Endpoints

All loopback-only; foreign `Origin` / `Host` headers are rejected.

- `GET /` — rendered HTML page
- `GET /reload` — current render version (used by the plugin's readiness probe)
- `GET /ws` — WebSocket upgrade for live reload + scroll sync
- `POST /render` — re-render (optionally switching `file`, restricted to the originally-served directory)
- `POST /scroll` — broadcast a scroll target line

## Repo layout

```
cmd/mdp/main.go    -- CLI entrypoint + `mdp serve` subcommand
internal/render    -- markdown → HTML body + page template
internal/server    -- HTTP + WebSocket server for the plugin
internal/config    -- TOML config, browser detection, fzf picker
```
