# goforj/web

Minimal app-facing HTTP abstraction for GoForj.

Current scope:

- `Context`
- `Handler`
- `Middleware`
- `Router`

This repo is intentionally starting narrow. Echo will be the first adapter, but app code should depend on `github.com/goforj/web`, not the adapter directly.

First adapter:

- `github.com/goforj/web/adapter/echoweb`
