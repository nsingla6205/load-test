# ONTAP Host Selection: Fast-Probe Improvement (OCI reset password API)

## Summary

When VCP builds an ONTAP REST client for a pool, it tries the pool's management
LIFs (node endpoints) one by one and applies the **full retry budget** to each
host while deciding whether it is reachable. A single unhealthy LIF therefore
costs ~55 seconds before failover, and with many unhealthy LIFs the cost grows
linearly and can exceed the activity and workflow timeouts.

This doc describes the issue and two ways to fix it:

1. **Sequential two-phase fast-probe** — separate host *selection* (cheap, no
   retries) from the actual *operation* (full retries on the chosen host).
2. **Parallel fast-probe** — same phase split, but probe all hosts at once and
   take the first that responds.

**Status:** Approach A is implemented in
`core/ontap-rest/rest_client_fast_select.go`. It runs only when the caller opts in
(`FastHostSelect`, set today only by the OCI reset-password path) **and**
`ONTAP_REST_FAST_HOST_SELECT` is enabled. The probe deadline is 5s on OCI (see
[Probe timeout: why 5s](#probe-timeout-why-5s)).

---

## The issue

### What we observed

In the OCI reset-password workflow, the log lines were repeated warnings:

- `ontap-rest request retry` / `ontap-rest request retry exhausted`
- probe `GET /api/cluster?fields=version` to a management LIF failing with
  `read: connection reset by peer`.

The workflow still **completed successfully** — the client failed over to a
healthy peer node and the password was set and expired. But each unhealthy LIF
burned ~55 seconds of retries first.

### Why it happens

`NewClient` (`core/ontap-rest/rest_client.go`) iterates the pool's hosts. For
each host it builds a transport that already includes the retry layer, then runs
a connectivity probe (`TestConnection` → `ClusterGet`) **through that retry
layer**. Only after the retries are exhausted does it move to the next host.

- Retry budget: `ONTAP_REST_RETRIES=5`, `ONTAP_REST_RETRY_WAIT_SECONDS=3`
  (`core/ontap-rest/transport/retry.go`).
- `connection reset by peer` is treated as a transient, retryable error, so a
  persistently bad LIF is retried 5 times (~55s total including connect time)
  before failover.
- `connection refused` is worse: it uses the long wait
  (`ONTAP_REST_RETRY_LONG_WAIT_MINUTES=1`), i.e. ~5 minutes per bad host.

The retry budget — meant to ride out a transient blip on a host we are actually
using — is being spent on **host discovery**.

### Key facts that make this matter

- The client is rebuilt on **every provider call** (`core/vsa/storage.go`
  `getOntapClient`), so selection cost is paid per ONTAP API call, not once per
  workflow.
- Host order is a Go map range (`for ip := range params.Hosts`), so it is
  **non-deterministic** — the healthy host can be tried last on any attempt.
- Relevant timeouts for reset-password:
  - Activity `StartToCloseTimeout` = **3 min** (`RbacActivityStartToClose`).
  - Activity retries = **3** (`RbacActivityMaxAttempts`), non-retryable only for
    `PanicError` — so a timeout **is** retried with a fresh random host order.
  - Workflow run timeout = **10 min**
    (`RESET_OCI_ADMIN_PASSWORD_WORKFLOW_TIMEOUT_MINUTES`, default 10).

Once selection exceeds the 3-min activity budget, the activity times out, retries
in a new random order, and at high node counts can exhaust the 10-min workflow
budget — turning a "slow but working" operation into an outright failure.

### Impact at scale (production: up to 24 nodes)

Worst-case time to select a healthy host when unhealthy LIFs return
`connection reset` (~55s/host today):

| Unhealthy LIFs (of 24) | Today (worst case) |
|---|---|
| 1 | ~55s |
| 6 | ~5.5 min → exceeds 3-min activity |
| 12 | ~11 min → exceeds 10-min workflow |
| 23 | ~21 min → workflow fails |

---

## Common design: two-phase (selection vs operation)

Both proposed approaches share the same core idea: **stop spending the retry
budget on host discovery.**

- **Phase 1 — Selection (fail fast, no retry budget):** probe each candidate
  with a single attempt and a short deadline (`ONTAP_REST_HOST_PROBE_TIMEOUT_SECONDS`,
  5s on OCI). Pick a host that responds. A dead LIF now costs at most ~5s instead
  of ~55s.
- **Phase 2 — Operation (full retry budget on the chosen host):** build the real
  client against the selected host with the normal retry policy and run the
  actual calls (e.g. Set / Expire password). Retries now do their intended job:
  absorbing a transient blip on a host we already confirmed is reachable.

**When every probe fails:** the builder does **not** make a full-retry attempt.
It logs `fast host-select: no reachable ONTAP REST API host` and returns the last
probe error (together with a retry-enabled client for the last host tried, which
mirrors the legacy "all hosts failed" contract). Callers such as
`ResetLoginPassword` / `ExpireLoginPassword` check the error first, so the ONTAP
call is not made and the activity fails with a retryable error. Temporal then
retries the whole activity (`RbacActivityMaxAttempts` = 3, backoff 5s then 10s),
and each attempt runs host selection again in a new random order. Tolerance for
a transient blip therefore comes from the activity retries, not from a fallback
inside the client.

The two approaches differ only in **how Phase 1 probes** the hosts.

---

## Approach A: Sequential two-phase fast-probe

Probe hosts one at a time (random or sorted order), each with a short deadline,
and stop at the first success.

**Cost** (N nodes, D unhealthy, H healthy, `T_dead` = probe deadline = 5s,
`T_ok` ≈ 0.5–1s):

- Best: healthy host first → ~`T_ok`
- Average: `D/(H+1) × T_dead + T_ok`
- Worst: `D × T_dead + T_ok`

For 24 nodes, worst case is ~2 min (23 unhealthy × 5s) — under the 3-min
activity cap, so it succeeds on the first attempt.

### Pros
- Simple: ~10 lines, no concurrency.
- Deterministic and easy to reason about, test, and log.
- No extra socket/CPU burst.
- Fixes the production 24-node scenario on its own (21 min → ~2 min worst case).

### Cons
- Selection latency still scales linearly with the number of unhealthy LIFs
  tried before a healthy one (~1–2 min at 24 nodes when most LIFs are down).
- If unhealthy LIFs are silently unresponsive (hit the full 5s deadline each),
  the linear cost is most noticeable here.

---

## Approach B: Parallel fast-probe

Fetch the certificate once (cached), then probe all hosts concurrently; the first
host to respond wins and the rest are cancelled.

**Cost:** ~`T_ok` (~0.5s) regardless of node count, as long as one healthy host
exists. If all hosts are dead, ~`T_dead` (~5s), then the activity fails and is
retried as above.

For 24 nodes, selection is ~0.5s in essentially all live-node topologies and ~5s
to conclude "all dead."

### Pros
- Near-constant selection time regardless of node count or number of unhealthy
  LIFs.
- Detects total outage fast (~5s vs ~120s sequential at 24 nodes).
- Naturally prefers the fastest-responding (healthiest) LIF.

### Cons / complexities (the main reason to be careful)

1. **Goroutine lifecycle and cancellation.** Must cancel losing probes when the
   first succeeds (`context.WithCancel`), ensure losers observe cancellation and
   exit, and avoid goroutine leaks. Tear down / close the client connections that
   were not selected.
2. **Concurrency burst.** N concurrent TLS handshakes + sockets per selection.
   Because the client is rebuilt per provider call, a chatty operation across
   many concurrent workflows on one worker can spike to hundreds of simultaneous
   dials. Mitigation: cap in-flight probes (e.g. 8–10) with a worker pool.
3. **Shared setup / thundering herd.** Fetch the certificate/secret **once**
   before fan-out. The cert cache helps, but a cold cache + N concurrent builds
   could stampede OCI CAS / Vault.
4. **Non-determinism.** The winner varies run-to-run (fastest responder), which
   is slightly harder to reproduce from logs (though "fastest healthy LIF" is a
   useful property).
5. **Error aggregation.** When all probes fail, collect and summarize N errors
   into one meaningful message rather than losing detail.
6. **Testing burden.** Requires concurrency tests, race-detector runs, and
   deterministic mocks — more surface than the sequential version.

---

## Probe timeout: why 5s

### What the timeout bounds

`ONTAP_REST_HOST_PROBE_TIMEOUT_SECONDS` is the deadline for one Phase 1 probe. It
bounds the **whole** request — TCP connect, mutual-TLS handshake and
`GET /api/cluster?fields=version` — in a single attempt with no retry layer. It is
shorter than, and therefore overrides, the transport's 15s `TLSHandshakeTimeout`.
A host that has not finished within the deadline is logged as
`ontap-rest error … context deadline exceeded` and skipped.

### The trade-off

- **Too short:** a LIF that is alive but slow to finish a cold cert-auth handshake
  is wrongly skipped. If the whole cluster is slow at once (cold start, load
  spike), every probe fails and the reset fails although every LIF is up. 3s left
  little room for this.
- **Too long:** with sequential probing, the all-unreachable case costs
  `N × T` per activity attempt. This case is realistic at any node count: a
  network, NSG or proxy problem between the worker and the pool makes every LIF
  drop packets at once.

Worst case per activity attempt (no host reachable), against the 3-min activity
`StartToCloseTimeout`:

| Nodes | 3s | **5s** | 8s |
|---|---|---|---|
| 2 | 6s | **10s** | 16s |
| 24 | 72s | **120s** | 192s — exceeds 180s |

At 8s a 24-node pool would hit the activity timeout part-way through probing:
Temporal would report a `StartToClose` timeout instead of the clear
`fast host-select: no reachable ONTAP REST API host` error, and 3 attempts
(~9.25 min) would leave almost no margin under the 10-min workflow timeout.

At 5s:

- 24 nodes, all unreachable: 120s per attempt; 3 attempts + backoff (5s, 10s)
  ≈ 6.25 min, under the 10-min workflow timeout.
- Healthy hosts are unaffected: they answer in ~0.5–1s whatever the deadline.
- A dead LIF still fails over in ≤5s, versus ~55s (or minutes) on the full-retry
  path.

### Limit for larger pools

The deadline must satisfy `N × T < ~170s` (the 3-min activity budget minus time
for the operation itself):

| T | Largest N |
|---|---|
| 3s | 56 |
| 5s | 34 |
| 8s | 21 |

For pools larger than ~34 nodes, or to raise the deadline further, add a total
time budget for Phase 1 or probe only a random subset of hosts. Password
operations apply to the whole cluster, so any reachable management LIF is enough.

### Configuration

| Setting | Where | Value |
|---|---|---|
| `ONTAP_REST_FAST_HOST_SELECT` | OCI worker chart `workerConfig.ontapRestFastHostSelect` | `false` by default; enabled per environment (e.g. `overrides_eng_cp.yaml`) |
| `ONTAP_REST_HOST_PROBE_TIMEOUT_SECONDS` | OCI worker chart `workerConfig.ontapRestHostProbeTimeoutSeconds` | `"5"` |
| Code fallback | `defaultHostProbeTimeoutSeconds` in `rest_client_fast_select.go` | 3s, used when the variable is unset or non-positive |

Both values are read once at worker start-up. After changing the ConfigMap,
restart the worker pods; the chart does not roll them automatically.
