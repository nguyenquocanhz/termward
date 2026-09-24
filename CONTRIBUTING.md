# Contributing

Thanks for helping! Bug reports, ideas and pull requests are all welcome.

## Layout

| Path | What |
|---|---|
| `core/internal/sshx` | SSH connection pool, auth, known_hosts |
| `core/internal/health` | Health collector script, parser, thresholds, monitor & alerts |
| `core/internal/keys` | Key generation/import |
| `core/internal/api` | HTTP + WebSocket API used by the UI |
| `core/internal/app` | Wires everything; shared by desktop and mobile |
| `core/cmd/termwardd` | Desktop sidecar binary |
| `core/mobile` | gomobile binding for Android/iOS |
| `app/src` | React UI (all platforms) |
| `app/electron` | Desktop shell |
| `app/android`, `app/ios`, `app/native/ios-core` | Mobile shells |

## Before sending a pull request

```bash
cd core && gofmt -l . && go vet ./... && go test ./...
cd app && npm run typecheck && npx prettier --check "src/**/*.{ts,tsx,css}" "electron/**/*.ts"
```

- Keep UI strings in `app/src/lib/i18n.ts` for **both** English and Vietnamese
  (TypeScript fails the build if a key is missing in one of them).
- Anything that touches credentials or host keys needs a test.
- Small, focused pull requests are reviewed fastest.
