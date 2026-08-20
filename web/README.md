# Code Agent · Web SPA (Sprint 2.1)

Multi-tenant control console. Vite + React 18 + TypeScript + Tailwind +
shadcn-style primitives. Talks to the Go backend via Bearer JWT.

## Quick start

```bash
# 1. Install deps (one-time)
cd web
npm install

# 2. Dev server (proxies /api to the Go backend on :8080 by default)
npm run dev
# open http://localhost:5173

# Override the backend:
VITE_BACKEND=http://10.0.0.5:8080 npm run dev

# 3. Production build
npm run build
# static files in web/dist/
```

## Routes

| Path | Auth | Description |
| | | |
| `/signin` | public | login form (Sprint 1.2) |
| `/signup` | public | org + owner signup (Sprint 1.2) |
| `/verify?token=…` | public | email verify landing (Sprint 1.2) |
| `/device/approve` | session | RFC8628 device authorization (Sprint 1.4) |
| `/` | JWT | dashboard |
| `/account` | JWT | profile (Sprint 2.2 will add edit forms) |
| `/devices` | JWT | device list (Sprint 2.6) |
| `/mcp` | JWT | MCP server list, per-user (Sprint 2.5) |
| `/llm-keys` | JWT | LLM API keys, per-user (Sprint 2.5) |
| `/audit` | JWT | audit log, tenant-scoped (Sprint 2.7) |
| `/agent` | JWT | Agent preferences (Sprint 2.4) |
| `/settings` | JWT | misc settings |

## Architecture

- **State**: zero global store. Auth tokens live in `localStorage` and are
  exposed through `@/lib/api` (`getAccessToken`, `setTokens`, `clearTokens`).
- **API client** (`src/lib/api.ts`): single `api<T>(path, init)` wrapper.
  - Attaches `Bearer <jwt>` when an access token is present.
  - On `401`, attempts a single refresh (`POST /api/v1/auth/refresh`) and
    retries the original request once.
  - On second `401`, clears tokens and emits `auth-expired` (handled by
    `AuthBridge` in `App.tsx` to bounce to `/signin`).
- **Routing**: React Router 6 with a `<RequireAuth>` guard for protected
  routes. Public pages render without the `AppShell`.
- **UI**: shadcn-style primitives in `src/components/ui/*`. Tailwind
  classes; dark mode by default (`class="dark"` on `<html>`).

## Sprint roadmap

This is the Sprint 2.1 skeleton. Subsequent sprints fill in:
- 2.2 — account edit, password change, 2FA
- 2.4 — agent preferences form
- 2.5 — MCP server CRUD + LLM Key CRUD (form pattern + danger rules)
- 2.6 — device list + revoke
- 2.7 — audit filters, exports