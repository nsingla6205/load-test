# IC LIF Address-Space Management — Functional Test Suite

**Document Version:** 2.0
**Status:** Draft
**Component:** VCP / Pool networking (intercluster LIF address space)
**Design reference:** [doc/architecture/designs/0030-ic-lif-address-space.md](../../../architecture/designs/0030-ic-lif-address-space.md)
**Related suite:** [cross-region-replication-tests.md](../crr/cross-region-replication-tests.md)

---

## 1. Scope

| Item | Value |
|---|---|
| Feature | Managed intercluster LIF (IC LIF) address space: carve a secondary block from an operator-registered parent range, attach it to the pool data subnet via GCP `subnetworks.patch`, reserve per-pool IC LIF IPs |
| Total test cases | 51 |
| Areas covered | Feature flags, address range prerequisites, pool create/carve, subnet reuse, cross-region replication connectivity, pool delete, IP exhaustion, race conditions |
| Out of scope | ONTAP-internal LIF configuration, VLM cluster deployment failures unrelated to IC LIF, data LIF address ranges |

## 2. Test environment

| Setting | Where configured | Value under test |
|---|---|---|
| `IC_LIF_ADDRESS_SPACE_ENABLED` | vcp-worker env (`skaffold/k8s/vcp-worker.yaml`) | Toggled per test; gates the carve path |
| `ADDRESS_SPACE_MGMT_ENABLED` | core-api env | Toggled per test; gates the address-range CRUD API |
| `TOTAL_IP_PER_HA_PAIR` | vcp-worker env | 6 (default); unchanged by the flag |
| `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY` | vcp-worker env | 6 (default); drives large-capacity carve sizing |
| `DATA_SUBNET_CIDR_BLOCK_LV` | vcp-worker env | /26 (default); fixed, not sized per pool |
| `crrEnabled` | google-proxy feature flag | true (required for Area E) |
| GCP | Shared VPC host project | SN service account must hold `compute.subnetworks.update` on the host project |

The two flags are independent and must be tested as a matrix, not as a single switch.

## 3. Sizing reference

Primary subnet sizes are fixed by configuration and do **not** vary per pool: a standard pool gets `DATA_SUBNET_CIDR_BLOCK` (/28) and a large-capacity pool gets `DATA_SUBNET_CIDR_BLOCK_LV` (/26), with or without the flag.

What the flag changes is the carve. A standard pool shares its subnet, so its carve covers everything that subnet could ever hold. A large-capacity pool owns its subnet, so its carve covers exactly `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY` (or the allowlisted count).

| Pool type | Primary subnet | HA pairs carved for | IC IPs required | Carved secondary block |
|---|---|---|---|---|
| Standard | /28 (fixed) | 2 (subnet ceiling) | 4 | /29 (8 addresses) |
| Large capacity, 6 HA pairs | /26 (fixed) | 6 | 12 | /28 (16 addresses) |
| Large capacity, 8 HA pairs | /26 (fixed) | 8 | 16 | /28 (16 addresses) |
| Large capacity, 12 HA pairs | /26 (fixed) | 12 | 24 | /27 (32 addresses) |

With the flag off, a large-capacity pool keeps the same /26 and its IC LIFs stay inside it. The saving from the flag is the addresses reclaimed from the primary subnet by moving IC LIFs into the carved secondary range, not a smaller primary subnet.

**The primary subnet reuse check is unchanged.** `TOTAL_IP_PER_HA_PAIR` (6) still governs whether an existing subnet has room for another pool, with the flag on or off. The flag does not lower that threshold, so no reuse behaviour should differ between the two matrix arms.

**Capacity ceiling.** A /26 gives 60 usable addresses and real consumption is 5 per HA pair (2 `dataprimary`, 2 SAN, 1 ILB NAS). 12 HA pairs therefore fills a /26 exactly, with no spare, and 24 HA pairs does not fit at all. Treat 12 as the supported ceiling. Do not test 24 HA pairs against this build: it needs per-pool subnet sizing, which is deferred to separate follow-up work.

The per-pool reservation in `lifs` is `HA pairs x 2`, so a standard pool holding only 2 IPs out of an 8-address block is correct and is not a leak.

## 4. Setup steps referenced by the test table

| Setup ID | Action | Detail |
|---|---|---|
| S1 | Register a parent intercluster range | `POST /v1/addressRange` with body `{"addressRange":"vpc1-ic-range","addressRangeCidr":"192.168.1.0/24","network":"projects/<hostProjectNumber>/global/networks/<vpc>","lifType":"interclusterLIF"}`. Requires `ADDRESS_SPACE_MGMT_ENABLED=true`. |
| S2 | Verify carved block | `SELECT id, network, start_ip, end_ip, netmask, address_range_id, deleted_at FROM ip_ranges WHERE lif_type='interclusterLIF' AND network='projects/<hostProjectNumber>/regions/<Region>/subnetworks/<subnet>';` Expect `netmask` as dotted octet (e.g. `255.255.255.248` for a /29). |
| S3 | Verify reserved IC LIF IPs | `SELECT name, ip_address, node_id, ip_range_id, deleted_at FROM lifs WHERE type='interclusterLIF' AND pool_id=<poolID>;` |
| S4 | Verify parent range state | `SELECT address_range, address_range_cidr, address_range_state FROM address_ranges;` |
| S5 | Verify pool metadata | `SELECT cluster_details->'intercluster_lifs' FROM pools WHERE id=<poolID>;` |
| S6 | Verify GCP secondary range | `gcloud compute networks subnets describe <subnet> --region <region> --project <hostProject> --format="value(secondaryIpRanges)"` |

---

## 5. Test cases

| Area | Test ID | Priority | Description | Test Steps | Expectation |
|---|---|---|---|---|---|
| A. Feature flags | TC-ICLIF-FLAG-001 | P1 | Both flags off — legacy IC LIF path | 1) Set `IC_LIF_ADDRESS_SPACE_ENABLED=false` and `ADDRESS_SPACE_MGMT_ENABLED=false`. 2) Restart worker and core-api. 3) Create a standard pool. 4) Run S2, S3, S5, S6. | Pool reaches READY. No `ip_ranges` row with `lif_type='interclusterLIF'`. No `lifs` rows with `type='interclusterLIF'`. No secondary range on the GCP subnet. `cluster_details.intercluster_lifs` is still populated from the VLM config, so IC LIFs exist on ONTAP via the legacy path. |
| A. Feature flags | TC-ICLIF-FLAG-002 | P2 | Address-space API off, carve flag on | 1) Set `ADDRESS_SPACE_MGMT_ENABLED=false`, `IC_LIF_ADDRESS_SPACE_ENABLED=true`. 2) Attempt S1. 3) Create a standard pool. | The address-range API is unavailable or rejects the request. With no parent range resolvable, pool create falls back to the legacy path and still succeeds. Worker logs `resolveSecondaryIpRange: no active interclusterLIF address range for VPC; legacy IC path`. |
| A. Feature flags | TC-ICLIF-FLAG-003 | P1 | Carve flag off, parent range exists | 1) Set `ADDRESS_SPACE_MGMT_ENABLED=true`, `IC_LIF_ADDRESS_SPACE_ENABLED=false`. 2) Run S1. 3) Create a standard pool. 4) Run S2, S4. | Parent range is created and stays in `CREATED`. Pool create ignores it: no carve, no `ip_ranges` row, no reservation, no subnet patch. Worker logs `resolveSecondaryIpRange: skipping — IC LIF address space management disabled`. |
| A. Feature flags | TC-ICLIF-FLAG-004 | P1 | Both flags on — managed path active | 1) Set both flags true. 2) Run S1. 3) Create a standard pool. | Managed path runs end to end. Detailed assertions in TC-ICLIF-CREATE-001. |
| A. Feature flags | TC-ICLIF-FLAG-005 | P2 | Flag enabled after pools already exist | 1) With flags off, create pool P1. 2) Enable both flags, run S1, restart worker. 3) Create pool P2 on the same data subnet. 4) Run S2, S3 for both pools. | P1 is untouched: no retroactive carve, no `lifs` rows appear for it. P2 carves a block and attaches it to the shared subnet. P1 keeps operating and its ONTAP IC LIFs remain functional. |
| B. Address range | TC-ICLIF-ADDR-001 | P1 | Pool create with no interclusterLIF range | 1) Both flags on. 2) Confirm no `address_ranges` row with `lifType=interclusterLIF` for the VPC. 3) Create a standard pool. | Pool create **succeeds** on the legacy path. No carve, no `ip_ranges` row, no reservation. Log line `no active interclusterLIF address range for VPC; legacy IC path`. This is a graceful fallback, not an error. |
| B. Address range | TC-ICLIF-ADDR-002 | P2 | Pool create with a dataLIF-only range | 1) Create an address range with `lifType=dataLIF` only. 2) Create a standard pool. | Same outcome as TC-ICLIF-ADDR-001. The dataLIF range must never be used as an IC LIF parent. |
| B. Address range | TC-ICLIF-ADDR-003 | P1 | Pool create with a valid interclusterLIF range | 1) Run S1 with `192.168.1.0/24`. 2) Create a standard pool. 3) Run S2, S4. | Carve succeeds. `ip_ranges` row created with `lif_type='interclusterLIF'`, `network=projects/<hostProjectNumber>/regions/<Region>/subnetworks/<data subnet>`, dotted-octet `netmask`, `address_range_id` pointing at the parent. Parent transitions `CREATED` to `IN_USE`. |
| B. Address range | TC-ICLIF-ADDR-004 | P2 | Multiple non-overlapping ranges per VPC | 1) Create range A `192.168.1.0/24` and range B `192.168.2.0/24`, both interclusterLIF, same VPC. 2) Create pools until A is exhausted. 3) Create one more pool. 4) Run S2. | Both ranges are accepted (the single-range-per-VPC unique index was removed). Carving spills from A into B once A has no free block; the new pool's `ip_ranges.address_range_id` points at B. |
| B. Address range | TC-ICLIF-ADDR-005 | P1 | Overlapping interclusterLIF range rejected | 1) Create range A `192.168.1.0/24` (interclusterLIF). 2) Attempt to create range B `192.168.1.128/25`, same VPC and lifType. | Second create is rejected with a conflict. Overlapping IC LIF parents must never coexist, or two pools could be handed the same address space. |
| B. Address range | TC-ICLIF-ADDR-006 | P2 | DISABLED parent range is not used | 1) Create range A. 2) Move it to `DISABLED` via `PUT /v1/addressRange/{id}`. 3) Create a pool. | Disabled parent is skipped; only `CREATED` and `IN_USE` are eligible. With no other eligible parent the pool falls back to the legacy path as in TC-ICLIF-ADDR-001. |
| B. Address range | TC-ICLIF-ADDR-007 | P2 | Parent range too small to carve | 1) Create an interclusterLIF range of `/30`, smaller than the `/29` minimum block. 2) Create a standard pool. | Pool create fails closed **before** the data subnet is created. HTTP 409 with an address-space-exhausted message. No orphaned subnet left in GCP. |
| C. Create / carve | TC-ICLIF-CREATE-001 | P1 | Standard pool, new subnet, full happy path | 1) Both flags on, S1 done. 2) Create a standard pool. 3) Run S2, S3, S5, S6. 4) Query ONTAP `default-intercluster` LIFs. | Pool READY. GCP subnet has exactly one secondary range named `ic-lif-<carved-cidr>-<8-hex>` holding the carved /29. One `ip_ranges` row. Two `lifs` rows (`type='interclusterLIF'`, `node_id` non-zero after deploy, names `ic-lif-pool-<poolID>-<n>`). `cluster_details.intercluster_lifs` holds the same two IPs. ONTAP LIFs match. |
| C. Create / carve | TC-ICLIF-CREATE-002 | P1 | Large capacity pool carves a larger block | 1) Set `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY=6`. 2) Create a pool with `largeCapacity=true`. 3) Run S2, S3. | Carved block is a /28 (16 addresses) sized for 6 HA pairs at 2 IC IPs each, not a /29. `lifs` row count equals `HA pairs x 2`. |
| C. Create / carve | TC-ICLIF-CREATE-009 | P1 | Primary subnet size is unaffected by the flag | 1) Create a `largeCapacity=true` pool with `IC_LIF_ADDRESS_SPACE_ENABLED=true`. 2) Repeat with the flag off. 3) Create a standard pool under both settings. 4) Read the allocated subnets from GCP. | Large-capacity pools get `DATA_SUBNET_CIDR_BLOCK_LV` (/26) and standard pools get `DATA_SUBNET_CIDR_BLOCK` (/28) in every case. The flag changes the carve, never the primary subnet size. |
| C. Create / carve | TC-ICLIF-CREATE-010 | P1 | Allowlisted account carves for its larger cluster | 1) Set `EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY=12` and add the account to `EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS`. 2) Create a `largeCapacity=true` pool on that account. 3) Run S2, S3. | Carve covers 12 HA pairs (24 IC IPs, a /27), not the 12 IC IPs six pairs would take. `lifs` row count is 24. A non-allowlisted account on the same worker still carves for 6. This is the allowlist regression: the carve previously ignored the allowlist and under-carved. |
| C. Create / carve | TC-ICLIF-CREATE-003 | P1 | Carve order — subnet created before patch | 1) Enable worker debug logging. 2) Create a pool. 3) Trace the correlation ID through worker logs. | The subnet create request carries **no** `secondaryIpRangeSpecs`. A separate `subnetworks.patch` follows it, and `PersistInterclusterIPRange` runs only after the patch operation completes. |
| C. Create / carve | TC-ICLIF-CREATE-004 | P1 | Pre-check fails before subnet creation | 1) Fill the parent range so no free block remains. 2) Create another pool. 3) Check GCP for new subnets. | `CheckInterclusterCarveAvailability` fails the create before the subnet is requested. No data subnet is created, so nothing needs cleanup. HTTP 409. |
| C. Create / carve | TC-ICLIF-CREATE-005 | P1 | Patch failure leaves no reservation | 1) Force `subnetworks.patch` to fail (revoke `compute.subnetworks.update` on the SN service account). 2) Create a pool. 3) Run S2. 4) Restore the permission and retry the create. | Pool create fails. **No** `ip_ranges` row exists for the attempted CIDR, so the block stays available. Subnet rollback releases the subnet. The retry succeeds and carves the same block. |
| C. Create / carve | TC-ICLIF-CREATE-006 | P1 | Activity retry is idempotent | 1) Create a pool. 2) Force a Temporal retry of `EnsureInterclusterSecondaryRangeOp`, for example a transient GCP 5xx. 3) Run S2, S6. | The retry derives the same range name and CIDR for the same pool, GCP reports the range already attached, and the create proceeds. Exactly one `ip_ranges` row, one set of `lifs` rows, one secondary range on the subnet. |
| C. Create / carve | TC-ICLIF-CREATE-007 | P2 | VLM deploy failure rolls back reservation | 1) Create a pool. 2) Force the VLM cluster deploy to fail after IC LIF reservation. 3) Run S3. | `lifs` rows for the pool are soft-deleted by rollback. No orphaned reserved IPs remain that would shrink the usable address pool. |
| C. Create / carve | TC-ICLIF-CREATE-008 | P2 | Carved IPs exclude network and broadcast | 1) Create a pool. 2) Run S3. 3) Compare the IPs against the carved /29 bounds from S2. | Reserved IPs are host addresses inside the carved block. The network and broadcast addresses of the block are not handed out. |
| D. Subnet reuse | TC-ICLIF-REUSE-001 | P1 | Second pool reuses the existing carve | 1) Create pool P1 (carves block B). 2) Create pool P2 on the same data subnet. 3) Run S2, S6. | No second carve and no second `subnetworks.patch`. P2 reuses the same `ip_ranges` row (identical `id`). Only one secondary range on the subnet. |
| D. Subnet reuse | TC-ICLIF-REUSE-002 | P1 | Reused carve hands out disjoint IPs | 1) After TC-ICLIF-REUSE-001, run S3 for both pools. 2) Run S5 for both pools. | P1 and P2 hold **disjoint** IC LIF IPs from the same block. Their `cluster_details.intercluster_lifs` lists do not intersect. |
| D. Subnet reuse | TC-ICLIF-REUSE-003 | P2 | Subnet fills to its HA-pair ceiling | 1) Add pools to one /28 data subnet until its HA-pair capacity (3) is consumed. 2) Create one more pool. 3) Run S2. | The next pool is placed on a new data subnet, which gets its own carve. No pool is placed on a subnet whose carved block cannot serve it. |
| E. CRR connectivity | TC-ICLIF-CRR-001 | P1 | CRR between two managed-IC-LIF pools | 1) Both flags on, `crrEnabled=true`. 2) Register parent ranges in regions R1 and R2. 3) Create pool P1 in R1 and P2 in R2; create volume V1 in P1. 4) `POST /v1beta/projects/{pn}/locations/{R1}/volumes/{V1}/replications` targeting P2. 5) Poll the operation to completion. | Replication reaches a healthy mirrored state. `volume_replications.state_details` passes through `Cluster Peered`. The `ACCEPT_CLUSTER_PEER` job on the destination reaches `DONE`. |
| E. CRR connectivity | TC-ICLIF-CRR-002 | P1 | Peer addresses are the carved IC LIF IPs | 1) During TC-ICLIF-CRR-001, capture the `CreateClusterPeering` and `AcceptClusterPeering` activity inputs from worker logs. 2) Compare against S3 and S5 output for both pools. | Peer addresses sent to the **source** ONTAP equal P2's `cluster_details.intercluster_lifs`. Addresses sent to the **destination** equal the source cluster's live `default-intercluster` LIF IPs. Both sets match the `lifs` rows reserved from the carved blocks. |
| E. CRR connectivity | TC-ICLIF-CRR-003 | P1 | ONTAP cluster peer is healthy | 1) After TC-ICLIF-CRR-001, query the cluster peer on both clusters. | Source and destination cluster peers report `availability` of `available` (or `partial` during convergence) and `authentication_state` of `ok`. Direct evidence that the carved IC LIF addresses are routable between regions. |
| E. CRR connectivity | TC-ICLIF-CRR-004 | P2 | SnapMirror firewall covers the carved range | 1) During TC-ICLIF-CRR-001, inspect the GCP firewall rules created for SnapMirror. | Ingress is permitted on ports 10566, 11104 and 11105 covering the carved secondary range, so peering traffic to the IC LIF addresses is not blocked. |
| E. CRR connectivity | TC-ICLIF-CRR-005 | P1 | Data actually replicates | 1) Write a known dataset to V1. 2) Wait for a transfer to complete. 3) Break the mirror. 4) Inspect the destination volume contents. | Destination content matches source. End-to-end proof that intercluster traffic traversed the carved addresses, not merely that peering objects were created. |
| E. CRR connectivity | TC-ICLIF-CRR-006 | P2 | CRR with one legacy-path pool | 1) Create P1 with the flag off (legacy IC LIFs) and P2 with the flag on (carved IC LIFs). 2) Replicate V1 from P1 to P2. | Peering succeeds. Source IC LIFs are read live from ONTAP regardless of how they were assigned, so a mixed-mode pair is supported. |
| E. CRR connectivity | TC-ICLIF-CRR-007 | P2 | Negative — destination pool without IC LIFs | 1) Construct a destination pool whose `cluster_details.intercluster_lifs` is empty. 2) Attempt to create a replication into it. | Replication fails at the cluster-peering step with a clear error, rather than hanging or leaving a half-peered state. |
| E. CRR connectivity | TC-ICLIF-CRR-008 | P1 | Peering survives an unrelated pool delete | 1) P1 and P3 share a data subnet in R1; P1 is replicating to P2. 2) Delete P3. 3) Run S2 and re-check the replication. | P1's IC LIFs and the carved `ip_ranges` row survive. Replication stays healthy and continues to transfer. Related to TC-ICLIF-DEL-002. |
| F. Deletion | TC-ICLIF-DEL-001 | P1 | Delete the only pool on a subnet | 1) Create a single pool with a carve. 2) Delete it. 3) Run S2, S3, S4, S6. | `lifs` rows soft-deleted. `ip_ranges` row soft-deleted. Data subnet released in GCP. Parent `address_ranges` returns from `IN_USE` to `CREATED` because no carved children survive. |
| F. Deletion | TC-ICLIF-DEL-002 | P1 | Delete one pool while another uses the subnet | 1) Create P1 and P2 on a shared subnet. 2) Delete P1 only. 3) Run S2, S3, S4, S6. | P1's `lifs` are soft-deleted. The subnet is **not** released, the `ip_ranges` row is **not** deleted, and the parent stays `IN_USE`. Worker logs the skip. P2 keeps its IC LIF IPs and stays operational. |
| F. Deletion | TC-ICLIF-DEL-003 | P1 | Delete the last pool on a shared subnet | 1) From TC-ICLIF-DEL-002, delete P2 as well. 2) Run S2, S4, S6. | Full cleanup now runs: subnet released, `ip_ranges` soft-deleted, parent returns to `CREATED`. |
| F. Deletion | TC-ICLIF-DEL-004 | P1 | Concurrent delete of all pools on a subnet | 1) Create P1 and P2 on one subnet. 2) Issue both deletes simultaneously. 3) Run S2, S4, S6. | Both deletes succeed. Because every pool on the subnet is in `DELETING`, cleanup proceeds. Exactly one release of the subnet and the carved range; no double-delete errors. |
| F. Deletion | TC-ICLIF-DEL-005 | P1 | Carved block is reusable after delete | 1) Delete the only pool as in TC-ICLIF-DEL-001. 2) Create a new pool against the same parent range. 3) Run S2. | The freed block is carved again for the new pool. Deleting a pool must genuinely return address space, not leak it. |
| F. Deletion | TC-ICLIF-DEL-006 | P2 | Parent kept IN_USE while children survive | 1) Have two subnets carve two blocks from the same parent. 2) Delete all pools on one subnet only. 3) Run S4. | That subnet's `ip_ranges` row is deleted, but the parent stays `IN_USE` because the second carved block still references it. |
| F. Deletion | TC-ICLIF-DEL-007 | P2 | Address range delete blocked while in use | 1) With a pool holding a carve, attempt `DELETE /v1/addressRange/{id}` on the parent. | The delete is rejected while carved children exist, so a parent cannot be removed from under a live pool. |
| G. IP exhaustion | TC-ICLIF-EXH-001 | P1 | Pools created until the parent is exhausted | 1) Register a small parent, for example `192.168.1.0/27` (room for four /29 carves). 2) Create pools on distinct subnets until every block is taken. 3) Create one more pool. | Each pool up to capacity succeeds. The next create fails with HTTP 409 and a message of the form `intercluster LIF address space exhausted: no free block for 6 IPs in ranges [192.168.1.0/27]`. |
| G. IP exhaustion | TC-ICLIF-EXH-002 | P1 | Exhaustion leaves no partial state | 1) After TC-ICLIF-EXH-001, inspect GCP subnets. 2) Run S2, S3. | The failed pool created no data subnet, no `ip_ranges` row and no `lifs` rows. Existing pools are unaffected. Failure is closed and clean, not partial. |
| G. IP exhaustion | TC-ICLIF-EXH-003 | P2 | Large pool cannot fit remaining space | 1) Leave only a /29 free in the parent. 2) Attempt a `largeCapacity=true` pool, which at 6 HA pairs needs a /28. | Create fails with the 409 exhaustion error. A large pool must not be squeezed into a block too small for its HA pairs. |
| G. IP exhaustion | TC-ICLIF-EXH-004 | P1 | Recovery by adding a second parent range | 1) From the exhausted state in TC-ICLIF-EXH-001, add a second interclusterLIF range for the VPC. 2) Retry the failed pool create. 3) Run S2. | The create succeeds, carving from the new parent. `ip_ranges.address_range_id` points at the newly added range. No service restart required. |
| G. IP exhaustion | TC-ICLIF-EXH-005 | P1 | Recovery by deleting a pool | 1) From the exhausted state, delete one existing pool. 2) Retry the failed create. | The freed block is reused and the create succeeds. |
| G. IP exhaustion | TC-ICLIF-EXH-006 | P2 | Exhaustion is not retried indefinitely | 1) Trigger exhaustion. 2) Inspect the Temporal workflow history. | The error is non-retryable, so the workflow fails promptly instead of burning retries against a condition that cannot resolve itself. |
| H. Race conditions | TC-ICLIF-RACE-001 | P1 | Two pools created simultaneously, same subnet | 1) Both flags on, parent range with room for at least one block. 2) Fire two pool creates at the same instant against the same VPC and subnet. 3) Run S2, S6. | Both carve the same first free block, so exactly one pool wins. The loser fails closed with a 409 conflict from the subnet patch and writes no `ip_ranges` row. Exactly one secondary range and one `ip_ranges` row exist. The two pools must never both claim the same CIDR. |
| H. Race conditions | TC-ICLIF-RACE-002 | P1 | Concurrent creates request distinct range names | 1) During TC-ICLIF-RACE-001, capture the `AddSubnetSecondaryRangeOp` calls from worker logs. | The two pools request **different** range names for the same CIDR, each carrying a per-pool discriminator. This is what turns the second request into a detectable conflict instead of a silently accepted no-op. |
| H. Race conditions | TC-ICLIF-RACE-003 | P1 | Losing pool can be retried successfully | 1) After TC-ICLIF-RACE-001, retry the failed pool create. 2) Run S2, S3. | The retry sees the winner's carve and either reuses it (same subnet) or carves the next free block, then completes. The race produces a transient failure, not a permanent one. |
| H. Race conditions | TC-ICLIF-RACE-004 | P1 | Database rejects a duplicate carved block | 1) Bypassing the workflow, attempt to insert two active `ip_ranges` rows with the same `network`, `start_ip`, `end_ip` and `lif_type='interclusterLIF'`. | The second insert is rejected by `idx_ip_ranges_unique_ic_block` with a unique-constraint violation, surfaced as a resource conflict. This is the backstop for the non-atomic overlap check in `CreateIPRange`. |
| H. Race conditions | TC-ICLIF-RACE-005 | P1 | Many concurrent creates on a shared subnet | 1) Create one pool to establish the carve. 2) Fire four pool creates simultaneously onto that same subnet. 3) Run S3 for all pools. | All four reuse the single existing `ip_ranges` row. Every pool receives **distinct** IC LIF IPs. No pool is given an IP already held by another. |
| H. Race conditions | TC-ICLIF-RACE-006 | P2 | Concurrent create and delete on a shared subnet | 1) Have P1 and P2 share a subnet. 2) Simultaneously delete P1 and create P3 on the same subnet. 3) Run S2, S6. | No lost update. Either P3 reuses the carve and the subnet survives, or the delete completes first and P3 carves anew. The subnet is never released while P3 is using it, and the `ip_ranges` row matches reality. |
| H. Race conditions | TC-ICLIF-RACE-007 | P2 | Concurrent large and standard pool creates | 1) Leave room for exactly one /28 in the parent. 2) Simultaneously create one large capacity pool and one standard pool. 3) Run S2. | Both cannot claim overlapping space. One succeeds; the other either carves a non-overlapping block if space allows or fails closed with a 409 exhaustion or conflict error. The resulting `ip_ranges` rows must not overlap. |

---

## 6. Exit criteria

| # | Criterion |
|---|---|
| 1 | All Area A to D cases pass under both flag settings |
| 2 | Area E proves connectivity with a real cross-region data transfer, not only peering object creation |
| 3 | Area F leaves no leaked subnets, `ip_ranges` rows, `lifs` rows, or parent ranges stuck in `IN_USE` |
| 4 | Area G always fails closed with HTTP 409 and never leaves partial GCP state |
| 5 | Area H shows no case where two pools hold overlapping intercluster address space |
| 6 | All P1 cases pass; any P2 failure has a triaged defect with an agreed disposition |

## 7. Known gaps and risks

| Risk | Impact | Mitigation |
|---|---|---|
| The unique index catches exact duplicate blocks, not partial overlaps | A partially overlapping carve would not be rejected by the database alone | Carving is block-aligned and deterministic, so exact duplication is the realistic collision; GCP rejects overlapping secondary ranges as a second line of defence |
| No detach path exists for a GCP secondary range | If a range outlived its `ip_ranges` row, a later carve of the same block would fail closed until detached by hand | Covered by TC-ICLIF-DEL-001 and TC-ICLIF-DEL-005; escalate if a carve fails with a conflict on a block the database shows as free |
| Race tests depend on real timing | Concurrency cases may pass by luck if the requests do not truly overlap | Confirm from timestamps that the creates overlapped, and repeat each race case at least three times |
| Exhaustion tests consume real GCP subnets | Test runs can leave subnets behind if a case aborts mid-way | Use a dedicated VPC and small parent ranges; verify with S6 and clean up between runs |
| The large-capacity primary subnet is a fixed global /26 | 12 HA pairs fills it exactly with no spare, and 24 HA pairs does not fit; the allowlist can raise the HA pair count past what the subnet holds | Treat 12 HA pairs as the supported ceiling and do not exercise 24 in this suite. Per-pool subnet sizing is deferred to separate follow-up work; until then a 24 HA pair cluster fails at SVM creation with a GCP `IP_SPACE_EXHAUSTED` error, not at create time |
