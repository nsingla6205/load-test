# Prod-like schema snapshots

The migration gate (`migration-gate.sh --snapshot <file>`) replays new
migrations on top of a **prod-like** schema so it reproduces drift-dependent
failures (e.g. `CREATE OR REPLACE VIEW` raising SQLSTATE 42P16 — the root cause
of the production "Dirty database version 40" incident). A fresh, empty DB does
**not** reproduce these.

## Generating a snapshot

```bash
# Option A — schema-only dump from a sanitized replica (never prod write traffic)
./schema-snapshot.sh from-db \
  --dsn 'postgresql://reader@replica:5432/vcp' \
  --out snapshots/vcp-prodlike.sql

# Option B — replay the migration chain of the already-deployed release, so the
# gate then applies only the NEW migrations on top (the real upgrade path)
./schema-snapshot.sh from-release \
  --ref origin/release-9.19 \
  --out snapshots/vcp-prodlike.sql
```

## Committing snapshots

- Schema-only dumps contain **no row data**, but still review them for anything
  sensitive (comments, role names) before committing.
- `*.sql` here is intentionally **not** gitignored so a vetted snapshot can be
  checked in for CI to use. Keep it schema-only.
