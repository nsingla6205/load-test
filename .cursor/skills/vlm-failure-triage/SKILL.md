---
name: vlm-failure-triage
description: Pull VLM/OCI stack logs via the oci-infra MCP and diagnose deployment failures against a known signature catalog. Use when a VSA cluster deployment fails, when asked to analyze vlm-worker logs, to triage OCI provisioning/cluster errors, or when the user pastes deployment logs.
---

# VLM Failure Triage

Diagnose VSA / OCI lifecycle-manager deployment failures from runtime logs.
Works purely from logs + the OCI infra MCP server (`oci-infra`) — no repo source
access required.

## When to use

- A deployment (e.g. `CreateVSAClusterDeploymentWorkflow`) failed or rolled back.
- The user asks to analyze `vlm-worker` (or other stack) logs.
- The user pastes deployment logs and wants a root-cause.

## Workflow

1. **Get the logs.** Call the `get_logs` tool on the `oci-infra` MCP server
   with the relevant `service` (default `vlm-worker`) and `tail` (default 500).
   If unsure the stack is running, call `status` first.
   - If the user already pasted logs, skip the fetch and analyze those.
2. **Scan** for `level=error` and `level=warning` lines and match them against
   `signatures.md`.
3. **Separate root cause from symptoms.** The first error in time is usually the
   root cause; later timeouts/rollback steps are downstream effects. Example: a
   `WaitForClusterHAReadyActivity` ScheduleToClose timeout is almost always a
   *symptom* of an earlier HA-interconnect / storage-failover failure.
4. **Report** using the output template below. Always cite the actual log lines
   (with timestamps) as evidence.
5. If the signature is unknown, say so explicitly and describe the failing
   workflow/activity + error text rather than guessing.

## Stack services (valid `service` values for get_logs)

`vlm-worker`, `oci-proxy`, `vcp-worker`, `temporal`, `postgres`, `ontap-sim`

- `vlm-worker` — VLM Temporal workflows/activities (most deployment failures).
- `oci-proxy` / `vcp-worker` — OCI cloud-provider calls.
- `ontap-sim` — simulated ONTAP cluster (cluster-create / HA / SFO behavior).

## Output template

```markdown
## Root cause
<one or two sentences>

## Evidence
- <timestamp> <verbatim log line fragment>
- ...

## OCI checks to run
1. <specific OCI-side check>

## Suggested next step
<the single most useful action>
```

## Signature catalog

Match errors against the catalog in [signatures.md](signatures.md). Add new
entries there whenever a novel failure is root-caused.

## Examples

**Invoke the skill** by naming it, optionally with a log file or a service:

```
/vlm-failure-triage /path/to/deployment-logs.txt   # analyze pasted/exported logs
/vlm-failure-triage vlm-worker                      # fetch live logs via the MCP
/vlm-failure-triage                                 # defaults to vlm-worker, tail 500
```

**What the skill then does:**

1. If no logs were provided, fetches them:
   `get_logs(service="vlm-worker", tail=500)` on the `oci-infra` MCP server
   (running `status` first if it's unclear the stack is up).
2. Scans for `level=error` / `level=warning` lines and matches them against
   `signatures.md`.
3. Picks the *earliest* matching error as root cause and treats later
   timeouts/rollbacks as symptoms.
4. Emits a report in the output-template format.

### Example 1 — matched signature (HA interconnect)

A deployment rolls back with a `WaitForClusterHAReadyActivity` timeout. The
earliest error matches the **HA interconnect / storage failover** signature, so
that — not the later timeout — is reported as the root cause:

```markdown
## Root cause
HA interconnect failure: the even node can't reach its partner over the `ic`
LIF, so ONTAP never enables storage failover and HA stays down.

## Evidence
- 08:21:43 Error Code: Failed to enable storage failover service. Reason:
  Cannot access partner via HA interconnect.
- 08:24:47 HA not yet enabled for 2/2 node(s)
- 08:34:47 WaitForClusterHAReadyActivity ... ScheduleToClose timeout (symptom)

## OCI checks to run
1. NSG/security-list on the `ic` subnet between ic_local_ip and ic_partner_ip.
2. Source/dest check disabled on the `ic` VNICs.

## Suggested next step
Run OCI Network Path Analyzer ic_local_ip -> ic_partner_ip; fix the ic-subnet
NSG rule, then redeploy.
```

### Example 2 — novel failure, then catalog growth

A different deployment where the cluster and HA come up fine but it rolls back
later during data-aggregate creation. No catalog entry matched at first, so the
skill reported the failing activity verbatim instead of guessing, root-caused
it, and **added a new entry to `signatures.md`** so it matches next time:

```markdown
## Root cause
Data disks attached on the OCI side but ONTAP discovered 0 disks (needed 4), so
aggregate creation timed out. On VSA/OCI the data disks are presented over the
iSCSI data path — ONTAP seeing 0 disks means that path is broken.

## Evidence
- 18:37:19 storage failover status is true   (HA/SFO succeeded — not the cause)
- 18:37:35 Disk attached successfully ... *-disk-data-0/1   (OCI attach OK)
- 18:48:10 [HA pair 1] Total unique disks collected: 0 (required: 4)
- 18:49:36 FetchUnassignedDisksForActiveActive ... ScheduleToClose timeout
- 18:51:43 failed to create data aggregates -> DeleteVSAClusterDeploymentWorkflow

## OCI checks to run
1. NSG/security-list on the iSCSI subnet between iscsi_local_ip / iscsi_partner_ip.
2. Source/dest check disabled on the `iscsi` VNICs.

## Suggested next step
Run OCI Network Path Analyzer iscsi_local_ip -> iscsi_partner_ip; fix the
iSCSI-subnet NSG rule, then redeploy.
```

This is the key behavior to review: on an unknown signature the skill never
guesses — it describes the failing workflow/activity verbatim, and once
root-caused it grows `signatures.md` so the catalog improves over time.
