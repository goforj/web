# goforj/web

Minimal app-facing HTTP abstraction for GoForj.

## Attribution

This library is inspired by Echo and started as an adapter-oriented layer over it.

Echo has been an excellent reference point for:

- route registration ergonomics
- context and middleware shape
- practical web framework design

`goforj/web` is intentionally beginning from that proven foundation, with the goal of growing more first-class GoForj experiences over time while keeping application code decoupled from any single web engine.

Current scope:

- `Context`
- `Handler`
- `Middleware`
- `Router`

This repo is intentionally starting narrow. Echo will be the first adapter, but app code should depend on `github.com/goforj/web`, not the adapter directly.

First adapter:

- `github.com/goforj/web/adapter/echoweb`

## Compatibility

The Echo bridge helpers in `adapter/echoweb` are kept for migration, but they are not the preferred long-term API.

Prefer:

- `web.Handler`
- `web.Middleware`
- `webmiddleware`

Avoid introducing new uses of:

- `echoweb.WrapHandler`
- `echoweb.WrapMiddleware`

## Development

Run the focused adapter benchmark suite with:

```bash
make bench
```

Run the full test suite with:

```bash
make test
```
