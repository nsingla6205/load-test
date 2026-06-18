# Auto-Tiering Config Atomic Partial JSON Update Decision

## Overview

- `auto_tiering_config` on the `pools` table is stored as a single JSON/JSONB document. Both the **auto-tiering background sync job** and **customer update pool** flows need to change different keys inside that document.
- Previously, several code paths used **read → merge in memory → write the whole JSON** (or `UpdatedPool` / `Save` on the full pool row). Concurrent updates could overwrite unrelated keys—for example, a background job persisting stale `hot_tier_size_in_bytes` after the customer updated hot tier size, or the reverse for consumption fields.
- This decision defines **field ownership** and requires **atomic partial JSON updates** in the database layer so each writer only touches the keys it owns.

## Problem

`auto_tiering_config` holds both:

- **Background configuration** updated frequently by background jobs (consumption, threshold, tiering status).
- **Customer configuration** updated via update pool (hot tier size, hot tier auto-resize).

Typical lost-update race:

1. Background job reads pool (including `hot_tier_size_in_bytes` and consumption).
2. Customer update pool changes `hot_tier_size_in_bytes` in the DB.
3. Background job writes the full `auto_tiering_config` JSON from its earlier read, restoring the old hot tier size.

The same class of bug applies when `UpdatedPool` persists an in-memory `AutoTieringConfig` snapshot that was loaded at workflow start: fresh consumption written by sync can be wiped when the workflow completes.

**Root cause:** treating one JSON column as a single blob without writer-scoped, atomic patches.

## Solution

### Field ownership

| Owner | `auto_tiering_config` keys | Pool column (non-JSON) |
|--------|---------------------------|-------------------------|
| **Background sync** | `hot_tier_consumption`, `cold_tier_consumption`, `tiering_fullness_threshold`, `tiering_status` | — |
| **Customer / update pool** | `hot_tier_size_in_bytes`, `enable_hot_tier_auto_resize` | `allow_auto_tiering` |

Other keys (for example `bucket_name`) are set at pool create or other flows and are not part of these two patch APIs.

### Database APIs

1. **`UpdatePoolTieringConfig`** — patches **background-owned** keys only via atomic partial JSON update.

2. **`UpdateAutoTieringHotTierInfo`** — patches **customer-owned** hot tier keys only, using the same atomic merge pattern.

3. **`UpdatedPool`** — **`Omit("AutoTieringConfig")`** on `Updates` so completing a pool update (description, size, state, attributes, etc.) never writes tiering JSON. Customer hot tier fields are persisted via **`UpdateAutoTieringHotTierInfo`** before or alongside `UpdatedPool`.

Non-nil pointer arguments mean “set this key to this value”; omitted pointers are not patched.

## Benefits

1. **No cross-writer clobbering** of tiering JSON keys when background sync and customer update overlap.
2. **Narrower writes** than `Save` / full-row `Updates` on `auto_tiering_config`—other pool columns are not at risk from tiering persistence.
3. **Clear ownership** for new `auto_tiering_config` fields: assign to background or customer patch API, not both via full-document merge.

## Trade-offs

- **`updated_at` on every patch** — `UpdatePoolTieringConfig` and `UpdateAutoTieringHotTierInfo` always set `updated_at` when a patch runs, including when the background sync job updates consumption on a schedule. That can advance the pool row timestamp more often than before.
- **From a DB perspective this is acceptable** — any real update to the row should refresh `updated_at`, even if updates are frequent. Partial JSON patches are still narrow writes; bumping the timestamp reflects that the pool record changed and is consistent with normal row-update semantics.

## Example scenarios

### Scenario 1: Customer hot tier resize during background consumption sync

**Setup:**

- DB: `{ hot_tier_size_in_bytes: 5 TiB, hot_tier_consumption: 100, cold_tier_consumption: 200 }`
- Background job computes new consumption `300` / `400` from ONTAP.
- Customer update pool sets hot tier size to `12 TiB`.

**With partial patches:**

1. Customer: `UpdateAutoTieringHotTierInfo` → `{ hot_tier_size_in_bytes: 12 TiB, ... }` (consumption unchanged).
2. Background: `UpdatePoolTieringConfig` → `{ hot_tier_consumption: 300, cold_tier_consumption: 400 }` (size unchanged).

**Result:** Final JSON has `12 TiB` size and `300` / `400` consumption.

**Without partial patches (old behavior):** whichever full JSON write runs last drops the other side’s change.

---

### Scenario 2: Update pool completes without touching sync fields

**Setup:**

- Workflow loaded `dbPool` at start with consumption `100` / `200`.
- Sync updates consumption to `500` / `600` in DB mid-workflow.
- Workflow only changes `description` and calls `UpdatedPool` with stale in-memory `AutoTieringConfig`.

**With `Omit("AutoTieringConfig")`:** Description updates; consumption stays `500` / `600`.

**Without `Omit("AutoTieringConfig")` (old behavior):** `UpdatedPool` could write consumption back to `100` / `200`.
