# Worker Versioning

## Introduction

In Temporal, workflows are long-lived and can run for days, months, or even years. As our service evolves, we'll inevitably need to update workflow and activity code. However, deploying new code can introduce non-determinism errors if running workflows encounter logic that no longer matches their original execution path. Traditionally, this requires complex patching and careful coordination to avoid breaking in-flight workflows.

To solve this, we will be deploying multiple worker versions in parallel. Each workflow will be pinned to the worker version, ensuring compatibility for in-flight executions during upgrades.

We will be following same versioning methodology for **VCP worker** and **VLM worker**. The only difference will be the criteria of choosing which versions to support at any point in time.

## Common Versioning Methodology

1. We will be having multiple deployments in the same helm chart.

2. Each deployment will be supporting a particular version.

3. Each deployment will have its own versioned config map as well.

4. For secrets, we have decided to use single one for all supported versions.
   - We will be following an incremental fill approach when adding new secrets, old ones will also remain.

5. We have defined a **version supported** (array type) field in the helm chart's override file.
   - The length of that array field will determine the number of deployments, ultimately telling the number of versions supported.

6. The helm chart deployments & config map are altered in such a way that they iterate over the above field & create deployments/configMaps.

7. The kubernetes service will always pick the last (i.e. the latest) deployment. Though there are no direct requests incoming to these worker pods, but this service is needed for metrics monitoring.

8. The temporal queues that we will be creating now will also be versioned. This will ensure that the versioned worker listens on its respective versioned queue, maintaining clear separability.

## VCP Worker Versioning

### Version Support Information

- At any point in time, we will support **3 versions** (one current & 2 previous versions).
- If version N is running, version N-1 & N-2 will also be running for supportability.
- These version will be none other than the chart build versions.
- Post this change, there is a one to one mapping between VCP worker & google proxy.
- Since the google proxy will be starting workflows on versioned queues, we will always need versioned workers listening on them.
- The google proxy & VCP worker will need to be upgraded hand in hand going forward.

### Usage

- Below is an example of how the version supported field will look like:

   ```yaml
   versionsSupported:
     - name: 25093.0.0-RC.14
       sha: 'f1eb02bd4dfec9e1676f38add8a9d37acb964c1f631c2cad9314979d05ab42fa'
     - name: 25093.0.0-DEV.22
       sha: 'a399697281ba0b7f5f1cfe0573d5f96e892d84cbde109fa7c3c34258e60c97d4'
     - name: 25093.0.0-DEV.27
       sha: '5cd8ff836da9a5e9dd372fe94cad5d80aaec3466f09a038de1f81f10aff860bb'
   ```

- Above, the `name` field shows which build version we are supporting.

- This build version is post-fixed with the VCP queue names used for VCP workflows.

**Important Notes:**
- This field in override file is **not to be edited manually**. Automated GitHub actions are written to modify this.
- When doing an env upgrade, only the chart version is changed, as was being done previously.
- No extra changes are needed (unless it's the first time a region is adopting worker versioning)

## VLM Worker Versioning

### Version Support Information

- At any point in time, we will support as many VLM worker versions as many ONTAP versions VCP will be supporting at that time.
- If VCP is supporting Ontap version X & Z, two vlm workers will be running, each supporting one.

**Rationale:**

The reason we followed this sort of versioning for VLM is listed below, as agreed by the VLM team:

1. There will be a 1:1 mapping between deployed VLM worker versions and ONTAP versions in production.
2. For every ONTAP version deployed in VCP, a corresponding VLM worker version will be deployed.
3. For urgent fixes that do not involve an ONTAP version change, the VLM worker image will be upgraded in-place.
4. All enhancements and new features will be bundled with main ONTAP releases and tied to a specific ONTAP version.

### Usage

- Below is an example of how the version supported field will look like:

   ```yaml
   ontapVersionVlmImageMappings:
     - ontapVersion: "9.17.1"
       vlmImageName: "vlm-worker"
       vlmImageTag: "R9.17.1Px_7825887"
       vlmImageDigest: "sha256:a1f1f3a9283a3ad5779069b8656b37e28219c125cb162314b837c80e7c6a1531"
       secondary: true
       pullPolicy: IfNotPresent
     - ontapVersion: "9.18.1"
       vlmImageName: "vlm-worker"
       vlmImageTag: "R9.18.1Px_8028694"
       vlmImageDigest: "sha256:b43b5ae0d471ab668458070235d8a021b9e5cc5cbca7f8274bade66f88a49201"
       secondary: true
       pullPolicy: IfNotPresent
     - ontapVersion: "9.18.1P2"
       vlmImageName: "vlm-worker"
       vlmImageTag: "R9.18.1x_8083481"
       vlmImageDigest: "sha256:35ed962d3209593086b29403dc158e36d3a84bc21eecd8c835ee2b6a17e37f02"
       secondary: true
       pullPolicy: IfNotPresent
     - ontapVersion: "9.18.1P3"
       vlmImageName: "vlm-worker"
       vlmImageTag: "R9.18.1x_8141647"
       vlmImageDigest: "sha256:2fdeb9e5828a74e047313178300fc6f8eb11a1a710bf04efa3115eb70daa860c"
       secondary: true
       pullPolicy: IfNotPresent
     - ontapVersion: "9.19.1"
       vlmImageName: "vlm-worker"
       vlmImageTag: "R9.19.1x_8201956"
       vlmImageDigest: "sha256:69f53f22a49dd354414ba461e77fd0abff1afb0d5417784cd60b348ec246aab2"
       secondary: true
       pullPolicy: IfNotPresent
   ```

- Above as you can see, we define the ontap version & vlm image details that support that particular ontap version.

- This ontap version is post-fixed with the VLM queue names used for VLM workflows.

**Important Note:**
- Whenever upgrading an env, ensure the above field are correct & up-to-date. They can come from the chart defaults or overrides file, if required.

**Reconciler:** To scale inactive `vlm-worker-*` Deployments to 0 after upgrade, see [0029-vlm-worker-reconciler-design.md](./0029-vlm-worker-reconciler-design.md).

## Cleanup Mechanism

1. The VCP & VLM worker deployments can be cleaned by removing them from the versions supported field.

2. Once above change is done, in next upgrade, those deployments will get automatically deleted by kubernetes.

3. Currently, config maps cleanup is manual. Once the deployment is deleted, the corresponding config map needs to be deleted as well.

---

**Source:** [Confluence - Worker Versioning for VSA Control Plane](https://confluence.ngage.netapp.com/spaces/VSCP/pages/1319653879/Worker+Versioning)

