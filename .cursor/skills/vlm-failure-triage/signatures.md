# VLM Failure Signature Catalog

Known failure signatures for VLM/OCI VSA deployments. Each entry: the log
signature to match, the root cause, and the OCI-side checks that confirm/fix it.

Grow this file as new failures are diagnosed. Keep root cause distinct from
downstream symptoms.

---

## HA interconnect / storage failover

**Signature**
```
Error Code: Failed to enable storage failover service.
Reason: Cannot access partner via HA interconnect.
```
Often followed by repeated `Join job failed for even node ...` and later
`HA not yet enabled for 2/2 node(s)`.

**Root cause**
The even node cannot reach its partner over the HA interconnect (the `ic` LIF).
ONTAP can't enable storage failover (SFO), so `ha.enabled` never becomes true.

**OCI checks**
1. NSG / security-list on the `ic` subnet — must allow node-to-node traffic
   between the two `ic` secondary IPs (look for `ic_local_ip` / `ic_partner_ip`
   in the `Updating extended metadata` log lines).
2. Source/dest check on the `ic` VNIC — usually must be disabled
   (`skipSourceDestCheck=true`); verify with `oci network vnic get`.
3. MTU consistency on the `ic` VNIC (jumbo-frame mismatch presents identically).
4. Confirm both `ic` secondary IPs exist and are attached (search log for
   `Secondary IP added to VNIC ... for LIFType ic`).
5. Fastest confirmation: OCI **Network Path Analyzer** from `ic_local_ip` →
   `ic_partner_ip`.

---

## Downstream HA-ready timeout (symptom, not root cause)

**Signature**
```
Failed to validate cluster HA status: activity error
(type: WaitForClusterHAReadyActivity ...): activity ScheduleToClose timeout
```

**Root cause**
Not a root cause on its own. The poller waited for `ha.enabled` on all nodes and
timed out because an earlier step (almost always the HA interconnect / SFO
failure above) never let HA come up. Trace back to the first
`AddEvenNodesToClusterWorkflow` error.

**Action**
Diagnose the earlier error; this timeout resolves once HA can be enabled.

---

## OCI API rate limiting

**Signature**
```
Http Status Code: 429. Error Code: TooManyRequests.
Message: Too many requests for the tenant
Operation Name: DeletePrivateIp   (or other operations)
```

**Root cause**
OCI tenancy-level API throttling, frequently seen during bulk rollback
(delete VMs / disks / IPs in parallel).

**OCI checks**
1. Usually transient — the operation is retried and succeeds (see following
   `... deleted successfully` lines).
2. If persistent: review OCI service limits and add backoff/throttling to the
   bulk delete path.

---

## Cluster not configured yet (benign, pre-create)

**Signature**
```
Cluster not configured yet on <ip> (401 on /cluster/jobs);
no in-progress create job to wait for
```
or `GET /cluster/nodes][401] ... Unauthorized` before the cluster exists.

**Root cause**
Expected: the cluster hasn't been created yet, so the management API rejects
auth. The workflow logs this as a warning and continues to cluster creation.

**Action**
Ignore unless it persists *after* `Cluster creation completed successfully`.

---

## Data aggregate creation — ONTAP sees 0 data disks (iSCSI data path)

**Signature**
```
[HA pair N] Found 0 unassigned disks for VM1 ...
[HA pair N] Found 0 spare disks for VM1 ...
[HA pair N] Total unique disks collected: 0 (required: 4)
```
followed by
```
Activity complete after timeout. ActivityType=FetchUnassignedDisksForActiveActive
  Error="context deadline exceeded (last poll error: ... insufficient disks for
  Active-Active ... found 0 (unassigned + spare), need 4)"
... FetchUnassignedDisksForActiveActive ... activity ScheduleToClose timeout
... failed to create data aggregates
```
and then `DeleteVSAClusterDeploymentWorkflow` rollback.

**Root cause**
Cluster + HA come up fine (`storage failover status is true`). The OCI data block
volumes (`*-disk-data-*`) are created and `Disk attached successfully` at the
OCI/hypervisor level, but ONTAP discovers **zero** disks, so it can never collect
the 4 disks needed to build the data aggregate and the poll times out. On VSA/OCI
the data disks are presented to ONTAP over the **iSCSI data path**; ONTAP seeing 0
disks means that path is broken (disks attached in OCI but not visible inside
ONTAP). This is the iSCSI-data analogue of the HA-interconnect failure — distinct
from it, and NOT a `WaitForClusterHAReadyActivity` problem.

**Distinguish from HA-interconnect failure:** here there is no
`Cannot access partner via HA interconnect`; SFO/HA succeed and the failure is
purely in `FetchUnassignedDisksForActiveActive` / `CreateAggregatesFor*HAWorkflow`.

**OCI checks**
1. NSG / security-list on the **iSCSI subnet** — must allow node-to-node traffic
   between the `iscsi_local_ip` / `iscsi_partner_ip` pair (see `Updating extended
   metadata` lines, e.g. `iscsi_local_ip:10.2.2.251 iscsi_partner_ip:10.2.2.146`).
2. Source/dest check on the `iscsi` VNICs — usually must be disabled
   (`skipSourceDestCheck=true`).
3. MTU consistency on the `iscsi` VNICs (jumbo-frame mismatch hides disks too).
4. Confirm all `*-disk-data-*` volumes actually attached (`Disk attached
   successfully`) AND reached `AVAILABLE`; if attached but invisible, suspect the
   iSCSI path, not the attach.
5. Network Path Analyzer from `iscsi_local_ip` → `iscsi_partner_ip` (both ways).

---

## Template for new entries

```markdown
## <short title>

**Signature**
\`\`\`
<verbatim log fragment>
\`\`\`

**Root cause**
<what actually went wrong>

**OCI checks**
1. <check>
```
