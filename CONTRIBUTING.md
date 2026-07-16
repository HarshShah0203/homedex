# Contributing to Homedex

Thanks for your interest! Homedex is pre-alpha and moving fast — the easiest ways to help right now:

1. **Vote on connectors.** Open (or 👍) an issue for the integration you want next — the roadmap order is community-voted.
2. **Try it and file honest issues** once `v0.1.0` ships.
3. **Write a connector.** Connectors are the heart of Homedex and are deliberately tiny:

```go
type Connector interface {
    Kind() string
    Validate(ctx context.Context, cfg Config) error  // powers the "Test connection" button
    Scan(ctx context.Context, cfg Config) (Snapshot, error)
}
```

A connector returns a `Snapshot` (hosts/services/ports/routes/certs/domains with stable natural keys) and never touches the database — the diff engine handles the rest. A worked ~200-line example guide is coming with the first release; until then, `internal/connectors/docker` is the reference.

## Ground rules

- **Read-only forever.** Any PR that requests write scopes on a connected system, or ingests container env vars, will be declined regardless of feature value.
- No telemetry of any kind, including "anonymous stats."
- UI bar: every view should look good in a screenshot. Dense, dark-mode-first, designed empty states.
- Be kind. Reviews focus on the code, not the person.

## Dev setup

Coming with the first tagged release (Go 1.23+, Node 20+, `make dev`). Until then, expect churn on `main`.
