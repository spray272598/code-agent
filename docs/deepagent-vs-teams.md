# DeepAgent vs Teams

| Dimension | DeepAgent (`/deep`) | Teams (`/team` / parallel) |
|-----------|---------------------|----------------------------|
| Topology | **Sequential** Plan → Act → Reflect | **Parallel** explore ‖ verify → merge |
| Strength | Depth, consistency, fewer race edits | Breadth, multi-angle coverage |
| Weakness | Higher latency (serial phases) | Merge quality depends on merge step |
| Tools | Phase-scoped allowlists | Role-scoped allowlists (`teams/default.yaml`) |
| Isolation | Same session context chain | Subagents / optional worktree |
| Best for | Single feature end-to-end | Investigation + verification fan-out |
| Resume | Session checkpoint (HITL interrupt) | Each subagent independent |

## Routing

```text
/deep  add code_search tool and unit tests
/team  how is permission Guard structured?
/compare-agents   # slash, no LLM
```

## Implementation map

| Mode | Code |
|------|------|
| Deep | `internal/domain/deepagent` + `einoorch.MultiAgent.RunDeep` |
| Teams | `internal/domain/team` + `einoorch.MultiAgent.RunParallel` + `subagent.delegate` |

## Rule of thumb

- **DeepAgent**: one coherent change must land (edit + test + review).
- **Teams**: concurrent exploration and critique of an existing codebase.
