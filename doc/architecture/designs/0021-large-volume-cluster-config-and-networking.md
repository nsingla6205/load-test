# Large Volume (LV) Cluster Configuration & Networking Sizing

## 1. Overview

This document describes how we configure and reason about **Large Volume (LV)** deployments (FlexGroup-based volumes requiring **multiple HA pairs**) from the VSA control plane perspective:

- **Cluster shape/config knobs**: HA pair count, CV defaults, tiering-dependent behaviors.
- **Sizing inputs/outputs**: how customer requirements (capacity/IOPS/throughput) translate into per-node requirements.
- **Networking sizing & prerequisites**: IP/subnet sizing knobs and common failure modes (high-level, deployment-oriented).

This doc intentionally stitches together the LV “subsystems” that are currently documented separately:

- **VM right-sizing (VMRS) for LV**: `doc/architecture/decisions/0006-large-volume-vmrs-decision-maker.md`
- **Constituent Volume (CV) placement algorithm**: `doc/architecture/designs/0018-cv-placement-logic.md`
- **CV placement known behaviors/limitations**: `doc/architecture/designs/0019-cv-placement-known-behaviors.md`

## 2. Goals / Non-goals

### 2.1 Goals

- Provide a single place to understand **LV cluster shape**, the **configuration knobs** that control it, and how those knobs affect:
  - VMRS selection/scaling
  - CV placement and constraints
  - networking prerequisites (CIDR sizing, IP-per-HA-pair assumptions)
- Make it easy to find where settings are configured (config YAML, Helm, local skaffold manifests).

### 2.2 Non-goals

- Re-document the internal VMRS algorithm details (covered by ADR-0006).
- Provide a full cloud-provider networking guide (VPC/PSC/peering/etc.). We capture **control-plane-facing prerequisites** and where to plug in values.

## 3. Terminology

- **HA pair**: two ONTAP nodes in HA. LV deployments involve multiple HA pairs.
- **Node**: a single ONTAP node. In many LV contexts, \(nodes = 2 × HA\_pairs\).
- **Aggregate**: storage aggregate; in LV cluster math we commonly treat aggregates as one-per-HA-pair.
- **CV (Constituent Volume)**: FlexGroup splits a large volume into multiple CVs distributed across aggregates.
- **Tiering / AllowAutoTiering**: affects CV placement constraints (CV-count-based vs space-based placement).

## 4. LV cluster shape & configuration model

### 4.1 Primary LV knob: HA pair count

The LV cluster shape is driven by a fixed HA-pair count:

- **Env**: `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`
  - **Default (local skaffold example)**: `6`
  - **Wired via**: `kubernetes/vcp-worker-chart/templates/configMap.yaml` and `skaffold/k8s/vcp-worker.yaml`

Practical implications:

- Affects how we compute per-node requirements after applying LV scaling factors (VMRS).
- Affects aggregate count expectations in CV placement (see `0018-cv-placement-logic.md`).

### 4.2 LV scaling-mode selection (VMRS)

- **Driven per-pool by the pool's `isActiveActive` (true-zonal) flag**, not by any env var.
  - active-active (true-zonal) => use `non_linear_scaling_active_active` from VMRS config
  - active-passive (default) => use `non_linear_scaling_active_passive` from VMRS config
  - **Resolved in**: `core/vmrs/decision/topology_resolver.go` (`ResolveScalingTopology`); the resolved topology selects the table in `applyNonLinearScaling` (`core/vmrs/decision/least_cost_large_volume_cluster.go`).

### 4.3 CV defaults (FlexGroup layout)

From CV placement docs:

- **Default constituents per aggregate**: `DEFAULT_CONSTITUENTS_PER_AGGREGATE` (documented in `0018-cv-placement-logic.md`)
- **Default total CVs**: aggregates × default-per-aggregate (e.g., 6 aggregates × 8 = 48 in the current doc)
- API can override via request field (documented as `LargeVolumeConstituentCount` in `0018-cv-placement-logic.md`)

### 4.4 CV constraints / limits (placement)

Key limits described in `0019-cv-placement-known-behaviors.md`:

- **Per-volume per-aggregate limit** (e.g., 200): env `MAX_CONSTITUENT_VOLUMES_PER_VOLUME_PER_AGGREGATE`
- **Total CVs per aggregate**: instance-type dependent (varies by VM type)

## 5. Performance & capacity sizing (control-plane view)

### 5.1 Inputs

At a high level, the sizing inputs come from a customer request:

- **Capacity**
- **IOPS**
- **Throughput**
- Whether the pool/volume is LV and whether **tiering** is enabled

### 5.2 VMRS configuration and strategy

The VMRS “source of truth” config for GCP is:

- **File**: `config/vmrs_gcp.yaml`

It includes:

- Strategy selection:
  - `least_cost_single_vm`
  - `least_cost_large_volume_cluster`
- Non-linear scaling factor tables:
  - `non_linear_scaling_active_passive`
  - `non_linear_scaling_active_active`
- ONTAP overhead amplification factors and headroom
- Qualified VM limits and relative cost (disk + ONTAP perf caps)

### 5.3 LV sizing logic (high-level)

For LV:

1. Select scaling table based on the pool's resolved topology (`isActiveActive` / true-zonal): active-active uses `non_linear_scaling_active_active`, otherwise `non_linear_scaling_active_passive`.
2. Apply **non-linear scaling** for the configured HA pairs (no interpolation; exact keys required as described in ADR-0006).
3. Divide scaled requirements by HA pair count to get **per-node/per-HA-pair requirements**.
4. Select the cheapest qualified VM type that satisfies those per-node/per-HA-pair requirements.

For details and examples, see:

- `doc/architecture/decisions/0006-large-volume-vmrs-decision-maker.md`

## 6. Networking sizing & prerequisites (LV-focused)

This section documents the knobs and sizing guidance the control plane uses (or expects to be provided), without prescribing a single cloud architecture.

### 6.1 Config knobs and where they come from

The worker chart exposes LV-related network sizing knobs in its ConfigMap template:

- `TOTAL_IP_PER_HA_PAIR`
- `DATA_SUBNET_CIDR_BLOCK` (non-LV)
- `DATA_SUBNET_CIDR_BLOCK_LV` (LV)
- `DATA_SUBNET_CIDR_BLOCK` / `DATA_SUBNET_CIDR_BLOCK_LV` are also present in the local skaffold manifest `skaffold/k8s/vcp-worker.yaml`

Interpretation (control-plane view):

- `TOTAL_IP_PER_HA_PAIR` is the per-HA-pair IP allocation assumption used when planning data subnet sizing.
- `DATA_SUBNET_CIDR_BLOCK_LV` is the subnet size to use (as CIDR prefix length) for LV data networks.

### 6.2 Sizing guidance

When sizing LV data networking, ensure there is enough address space for:

- **Per HA pair**: `TOTAL_IP_PER_HA_PAIR` × `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`
- **Plus headroom** for:
  - service IPs / load-balancer frontends (if used by the deployment model)
  - future expansion / retries / blue-green cluster operations

Recommended practice:

- Treat `DATA_SUBNET_CIDR_BLOCK_LV` as a **capacity planning** parameter and validate it early (before provisioning) against the configured HA pair count and total IP-per-HA-pair assumption.
- If failures are observed during LV provisioning that look like network exhaustion, confirm:
  - the subnet has sufficient free IPs
  - the configured `DATA_SUBNET_CIDR_BLOCK_LV` matches the intended cluster size

### 6.3 Connectivity prerequisites (high-level)

Document (per environment) the connectivity assumptions required for LV provisioning to succeed:

- Worker(s) must be able to reach the cloud provider management APIs (control plane endpoints).
- Worker(s) must be able to reach ONTAP/GCNV endpoints required to:
  - query aggregates/state
  - create and manage volumes/CVs

Where to capture environment specifics:

- Add a short “deployment environment appendix” in this doc (or a referenced runbook) that lists:
  - whether Private Service Connect / peering is used
  - which networks/subnets host the data-plane and management-plane components
  - firewall policy ownership and required allowlists

### 6.4 Common networking-related failure modes

- **IP exhaustion**: provisioning fails when trying to allocate addresses for LV data plane.
  - Check: `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`, `TOTAL_IP_PER_HA_PAIR`, and `DATA_SUBNET_CIDR_BLOCK_LV`.
- **Routing / firewall**: timeouts reaching ONTAP/GCNV or provider APIs.
  - Check: route tables / firewall policies / service attachments (environment-specific).

## 7. Where to configure LV behavior (repo locations)

### 7.1 VMRS config

- **GCP VMRS config**: `config/vmrs_gcp.yaml`
- Worker is pointed at this via env `VMRS_CONFIG_PATH` (see `kubernetes/vcp-worker-chart/templates/configMap.yaml` and `skaffold/k8s/vcp-worker.yaml`).

### 7.2 Worker runtime env (Helm)

- **Worker ConfigMap template**: `kubernetes/vcp-worker-chart/templates/configMap.yaml`
  - LV relevant keys include:
    - `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`
    - `TOTAL_IP_PER_HA_PAIR`
    - `DATA_SUBNET_CIDR_BLOCK_LV`
    - LV pool constraints used by orchestrator validation (examples):
      - `MIN_LV_THROUGHPUT`, `MAX_LV_THROUGHPUT`
      - `MAX_LV_POOL_CAPACITY`, `MAX_LV_HOT_TIER_POOL_CAPACITY`
      - `MIN_LV_POOL_COOL_TIER_CAPACITY`, `MIN_HOT_TIER_SIZE_LARGE_VOLUMES`

### 7.3 Local development (skaffold)

- **Local config manifest**: `skaffold/k8s/vcp-worker.yaml`
  - Provides sample values for LV-related env vars (HA pairs, LV subnet CIDR block, etc.)

### 7.4 Google proxy chart (CV placement knobs)

Some CV placement knobs are exposed via the google-proxy Helm chart ConfigMap:

- `kubernetes/vsa-control-plane/charts/google-proxy/templates/configMap.yaml`
  - includes `DEFAULT_CONSTITUENTS_PER_AGGREGATE`, `MAX_CONSTITUENTS_PER_VOLUME_PER_AGGREGATE`, etc.

## 8. End-to-end LV flow (high level)

1. **Request**: create pool/volume with LV attributes.
2. **VMRS sizing**:
  - apply overheads and non-linear scaling (based on HA pairs + per-pool topology from `isActiveActive`)
  - choose VM type for the cluster (ADR-0006)
3. **ONTAP state**:
  - fetch aggregates
4. **CV placement**:
  - if tiering enabled: CV-count-based placement
  - if tiering disabled: space-based placement
  - see `0018` and `0019`
5. **Provision**: create the volume with CV distribution and cluster configuration.

## 9. Troubleshooting pointers

- **VMRS scaling config errors** (missing scaling factors for configured HA pairs):
  - See ADR-0006; ensure `config/vmrs_gcp.yaml` has exact keys for the configured HA pair count.
- **CV placement failures**:
  - See `doc/architecture/designs/0019-cv-placement-known-behaviors.md`
  - Especially relevant when `AllowAutoTiering=false` (space-based constraints).
- **Networking failures**:
  - Validate LV subnet sizing (`DATA_SUBNET_CIDR_BLOCK_LV`), IP-per-HA-pair (`TOTAL_IP_PER_HA_PAIR`), and HA pair count.
  - Then validate environment-specific routing/firewall/PSC/peering prerequisites.

## 10. Appendix: Quick reference table (LV knobs)


| Knob                                          | Type               | Example / Default        | Where configured                          | LV impact                                                                  |
| --------------------------------------------- | ------------------ | ------------------------ | ----------------------------------------- | -------------------------------------------------------------------------- |
| `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`           | env                | `6` (local example)      | worker ConfigMap / skaffold               | cluster size; affects sizing + placement expectations                      |
| pool `isActiveActive` (true-zonal)            | per-pool attribute | resolved at request time | `core/vmrs/decision/topology_resolver.go` | selects scaling factor table (active-active vs active-passive) for LV VMRS |
| `config/vmrs_gcp.yaml` `non_linear_scaling_`* | yaml               | keys for 1/2/6/12        | config repo                               | non-linear perf scaling for LV clusters                                    |
| `TOTAL_IP_PER_HA_PAIR`                        | env                | `6` (local example)      | worker ConfigMap / skaffold               | IP planning per HA pair                                                    |
| `DATA_SUBNET_CIDR_BLOCK_LV`                   | env                | `26` (local example)     | worker ConfigMap / skaffold               | LV data subnet sizing                                                      |
| `DEFAULT_CONSTITUENTS_PER_AGGREGATE`          | env/chart value    | (chart-provided)         | google-proxy chart ConfigMap              | default CV layout for FlexGroup                                            |
| `MAX_CONSTITUENTS_PER_VOLUME_PER_AGGREGATE`   | env/chart value    | (chart-provided)         | google-proxy chart ConfigMap              | per-volume per-aggregate CV limit                                          |


