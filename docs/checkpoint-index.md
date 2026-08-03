# Checkpoint / Interrupt + Code Index

## Cross-process interrupt & checkpoint

| Piece | Role |
|-------|------|
| `internal/domain/checkpoint.Store` | Durable snapshot (file: `./data/checkpoints/{sessionId}.json`) |
| `RunRegistry` | In-process cancel map |
| `POST /api/v1/session/cancel` | Cancel active run + write `cancelled` snapshot |
| `GET /api/v1/session/checkpoint?sessionId=` | Load snapshot + `running` flag |
| `GET /api/v1/session/checkpoints?status=` | List (`interrupt` / `cancelled` / …) |
| Bootstrap restore | Rehydrate `pending` into Guard on restart |

**Flow**

1. Agent hits HITL → status `interrupt`, pending tool args persisted.
2. Process dies → restart restores pending via `RestoreCheckpoints`.
3. User approves + sends `继续` → resume path executes tool.
4. `POST .../cancel` → context cancel mid-run; checkpoint `cancelled`.

SSE: `checkpoint`, `cancel` events.

## Code index / Retriever

| Piece | Role |
|-------|------|
| `internal/domain/codeindex.Index` | Inverted index (token TF) over workspace |
| Tools | `code_search`, `code_index` |
| `GET /api/v1/index/search?q=` | HTTP search |
| `POST /api/v1/index/rebuild` | Rebuild |
| `GET /api/v1/index/stats` | File/token counts |

No external embedding API required — suitable for local Coding Agent demo and resume evidence.
