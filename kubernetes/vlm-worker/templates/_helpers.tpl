{{- define "imageRegistryFullPath" -}}
    {{- if eq .Values.global.primaryImageRegistryPath "" }}
        {{ .Values.global.chartPrimaryImageRegistry | default .Values.global.primaryImageRegistry }}
    {{- else }}
        {{ .Values.global.chartPrimaryImageRegistry | default .Values.global.primaryImageRegistry }}/{{ .Values.global.primaryImageRegistryPath }}
    {{- end -}}
{{- end -}}

{{- define "secondImageRegistryFullPath" -}}
    {{- if and ( ne .Values.global.secondaryImageRegistry "" ) ( eq .Values.global.secondaryImageRegistryPath "" ) }}
        {{ .Values.global.chartSecondaryImageRegistry | default .Values.global.secondaryImageRegistry }}
    {{- else if and ( ne .Values.global.secondaryImageRegistry "" ) ( ne .Values.global.secondaryImageRegistryPath "" ) }}
        {{ .Values.global.chartSecondaryImageRegistry | default .Values.global.secondaryImageRegistry }}/{{ .Values.global.secondaryImageRegistryPath }}
    {{- end -}}
{{- end -}}


{{/*
Helper function to get the final URL of the image to be used in the deployment.
*/}}
{{- define "containerImage" -}}
    {{- $context := index . 0 -}}
    {{- $args := index . 1 -}}
    {{- $imageName := index $args "vlmImageName" -}}
    {{- $vlmImageDigest := index $args "vlmImageDigest" -}}
    {{- $vlmImageTag := index $args "vlmImageTag" -}}
    {{- $isSecondary := index $args "secondary" -}}
    {{- $registry := ternary (include "secondImageRegistryFullPath" $context) (include "imageRegistryFullPath" $context) $isSecondary -}}
    {{- if $context.Values.global.useTags -}}
        {{- printf "%s/%s:%s" $registry $imageName $vlmImageTag -}}
    {{- else -}}
        {{- printf "%s/%s@%s" $registry $imageName $vlmImageDigest -}}
    {{- end -}}
{{- end -}}


{{/*
Helper function to generate the configMap name by appending version and "-config" to the app name.
Usage: include "vlm-worker.configMapName" (list $ $version)
*/}}
{{- define "vlm-worker.configMapName" -}}
    {{- $context := index . 0 -}}
    {{- $version := index . 1 -}}
    {{- printf "%s-%s-config" $context.Values.app.name $version -}}
{{- end -}}

{{/*
Helper function to generate task queue name based on ontap version.
*/}}
{{- define "vlm-worker.taskQueueName" -}}
{{- printf "%s-%s" .Values.workerConfig.taskQueuePrefix .Values.ontapVersion -}}
{{- end -}}

{{/*
Helper function to generate the secret name by appending "-secret" to the app name.
*/}}
{{- define "vlm-worker.secretName" -}}
{{- printf "%s-secret" .Values.app.name -}}
{{- end -}}

{{/*
Temporal Worker Deployment name. Override wins; otherwise TASK_QUEUE.
Dots are always replaced — Temporal forbids '.' in Worker Deployment names.
Usage: include "vlmWorkerVersioning.deploymentName" (list $ $taskQueue)
*/}}
{{- define "vlmWorkerVersioning.deploymentName" -}}
{{- $context := index . 0 -}}
{{- $taskQueue := index . 1 -}}
{{- $explicit := default "" $context.Values.vlmWorkerVersioning.temporalWorkerDeploymentName | toString | trim -}}
{{- $name := $explicit | default $taskQueue -}}
{{- $name | replace "." "-" -}}
{{- end -}}

{{/*
Build ROLLOUT_SPEC JSON for D-patch targets (mappings with buildId + drainingBuilds).
*/}}
{{- define "vlmWorkerVersioning.rolloutSpec" -}}
{{- $targets := list -}}
{{- $initials := list -}}
{{- range $mapping := .Values.ontapVersionVlmImageMappings -}}
  {{- $versionName := (regexFind `(?i)\d+\.\d+\.\d+(?:p\d+(?:x\d+)?|x\d+)?` $mapping.ontapVersion) -}}
  {{- $version := ($versionName | lower | replace "." "-") -}}
  {{- $buildId := (default "" $mapping.buildId) | toString | trim -}}
  {{- $taskQueue := printf "%s-%s" $.Values.workerConfig.taskQueuePrefix $versionName -}}
  {{- $deploymentName := include "vlmWorkerVersioning.deploymentName" (list $ $taskQueue) | trim -}}
  {{- if and $buildId $mapping.drainingBuilds -}}
    {{- range $drain := $mapping.drainingBuilds -}}
      {{- $old := (default "" $drain.buildId) | toString | trim -}}
      {{- if $old -}}
        {{- $drainDep := printf "%s-%s-drain-%s" $.Values.app.name $version $old -}}
        {{- $targets = append $targets (dict
          "ontapVersion" $versionName
          "taskQueue" $taskQueue
          "deploymentName" $deploymentName
          "newBuildId" $buildId
          "oldBuildId" $old
          "drainDeployment" $drainDep
          "drainReplicas" $.Values.app.replicas
          "releaseName" $.Release.Name
        ) -}}
      {{- end -}}
    {{- end -}}
  {{- else if $buildId -}}
    {{- $initials = append $initials (dict
      "ontapVersion" $versionName
      "taskQueue" $taskQueue
      "deploymentName" $deploymentName
      "buildId" $buildId
      "releaseName" $.Release.Name
    ) -}}
  {{- end -}}
{{- end -}}
{{- dict "targets" $targets "initialCurrents" $initials | toJson -}}
{{- end -}}

{{/*
cutoverPending is true when any drainingBuilds pair is not yet recorded in
<release>-versioning-cutover. The hook Jobs are omitted when this is false,
so a later upgrade does not start them. lookup is empty during helm template
with no cluster, which keeps the Jobs rendered.
Key format must match cutoverRecordKey in tools/vlm-worker-versioning-rollout.
*/}}
{{- define "vlmWorkerVersioning.cutoverPending" -}}
{{- $recorded := lookup "v1" "ConfigMap" .Release.Namespace (printf "%s-versioning-cutover" .Release.Name) -}}
{{- $pending := "false" -}}
{{- range $mapping := .Values.ontapVersionVlmImageMappings -}}
  {{- $versionName := (regexFind `(?i)\d+\.\d+\.\d+(?:p\d+(?:x\d+)?|x\d+)?` $mapping.ontapVersion) -}}
  {{- $buildId := (default "" $mapping.buildId) | toString | trim -}}
  {{- if and $buildId $mapping.drainingBuilds -}}
    {{- range $drain := $mapping.drainingBuilds -}}
      {{- $old := (default "" $drain.buildId) | toString | trim -}}
      {{- if $old -}}
        {{- $key := printf "%s__%s__%s" $versionName $buildId $old -}}
        {{- $have := "" -}}
        {{- if and $recorded $recorded.data (hasKey $recorded.data $key) -}}
          {{- $have = index $recorded.data $key | toString -}}
        {{- end -}}
        {{- if ne $have "done" -}}
          {{- $pending = "true" -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
  {{- else if $buildId -}}
    {{- $key := printf "%s__%s__initial" $versionName $buildId -}}
    {{- $have := "" -}}
    {{- if and $recorded $recorded.data (hasKey $recorded.data $key) -}}
      {{- $have = index $recorded.data $key | toString -}}
    {{- end -}}
    {{- if ne $have "done" -}}
      {{- $pending = "true" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- $pending -}}
{{- end -}}

{{/*
Helper function to conditionally include Cloud SQL Proxy sidecar container for the reconciler Job.
Only included when global.cloudSqlIamAuthEnabled is true.
Uses --quitquitquit + --admin-port so the reconciler binary can POST /quitquitquit to shut the
sidecar down cleanly when the Job finishes, preventing the pod from staying 1/2 NotReady.
*/}}
{{- define "vlmReconciler.databaseProxyContainer" -}}
{{- if .Values.global.cloudSqlIamAuthEnabled }}
{{- $instanceConnectionName := .Values.cloudSql.instanceConnectionName }}
{{- if .Values.global.coreConfig }}
{{-   if .Values.global.coreConfig.gcp }}
{{-     if .Values.global.coreConfig.gcp.instanceConnectionName }}
{{-       $instanceConnectionName = .Values.global.coreConfig.gcp.instanceConnectionName }}
{{-     end }}
{{-   end }}
{{- end }}
{{- if not $instanceConnectionName }}
{{-   fail "cloudSql.instanceConnectionName (or global.coreConfig.gcp.instanceConnectionName) must be set when global.cloudSqlIamAuthEnabled is true" }}
{{- end }}
- name: cloud-sql-proxy
  image: {{ .Values.global.cloudSqlProxy.image | default "gcr.io/cloud-sql-connectors/cloud-sql-proxy@sha256:f6abce8d90076c0857f23fb2cffb744024f380eca25334e9f2b52d2d74dc9967" | quote }}
  args:
    - "--private-ip"
    - "--auto-iam-authn"
    - "--structured-logs"
    - "--quitquitquit"
    - "--admin-port={{ .Values.vlmWorkerReconciler.cloudSqlProxy.adminPort }}"
    - "--port={{ .Values.vlmWorkerReconciler.database.port }}"
    - "{{ $instanceConnectionName }}"
  securityContext:
    runAsNonRoot: true
  resources:
    limits:
      cpu: 500m
      memory: 512Mi
    requests:
      cpu: 100m
      memory: 128Mi
{{- end }}
{{- end -}}
