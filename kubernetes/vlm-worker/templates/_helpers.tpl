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
        {{- $drainConfigMap := include "vlm-worker.configMapName" (list $ (printf "%s-drain-%s" $version $old)) | trim -}}
        {{- $drainImageName := default $mapping.vlmImageName $drain.vlmImageName -}}
        {{- $drainImageTag := default $mapping.vlmImageTag $drain.vlmImageTag -}}
        {{- $drainDigest := default "" $drain.vlmImageDigest | toString | trim -}}
        {{- if not $.Values.global.useTags -}}
          {{- $drainDigest = required (printf "drainingBuilds[%s].vlmImageDigest is required when global.useTags is false" $old) $drainDigest -}}
        {{- end -}}
        {{- $drainSecondary := ternary true false (default $mapping.secondary $drain.secondary | toString | eq "true") -}}
        {{- $drainPull := default $mapping.pullPolicy $drain.pullPolicy -}}
        {{- $drainImage := include "containerImage" (list $ (dict "vlmImageName" $drainImageName "vlmImageTag" $drainImageTag "vlmImageDigest" $drainDigest "secondary" $drainSecondary)) | trim -}}
        {{- $configData := dict
          "PROVIDER" ($.Values.workerConfig.provider | toString)
          "SERVER_IP" ($.Values.workerConfig.temporalHost | toString)
          "SERVER_PORT" ($.Values.workerConfig.temporalPort | toString)
          "TASK_QUEUE" $taskQueue
          "DEBUG" ($.Values.workerConfig.debug | toString)
          "MAX_CONCURRENT_ACTIVITIES" ($.Values.workerConfig.maxConcurrentActivities | toString)
          "MAX_CONCURRENT_WORKFLOWS" ($.Values.workerConfig.maxConcurrentWorkflows | toString)
          "MAX_CONCURRENT_WORKFLOW_TASK_POLLERS" ($.Values.workerConfig.maxConcurrentWorkflowTaskPollers | toString)
          "MAX_CONCURRENT_ACTIVITY_TASK_POLLERS" ($.Values.workerConfig.maxConcurrentActivityTaskPollers | toString)
          "GCE_METADATA_HOST" ($.Values.workerConfig.gceMetadataHost | toString)
          "ENABLE_GLOBAL_GCP_CLIENTS" ($.Values.workerConfig.enableGlobalGcpClients | toString)
          "USE_VM_RESERVATIONS" ($.Values.workerConfig.useVmReservations | toString)
          "TEMPORAL_VERSIONING_ENABLED" "true"
          "TEMPORAL_WORKER_BUILD_NUMBER" $old
          "TEMPORAL_DEPLOYMENT_NAME" $deploymentName
        -}}
        {{- $targets = append $targets (dict
          "ontapVersion" $versionName
          "taskQueue" $taskQueue
          "deploymentName" $deploymentName
          "newBuildId" $buildId
          "oldBuildId" $old
          "drainDeployment" $drainDep
          "drainConfigMap" $drainConfigMap
          "drainImage" $drainImage
          "drainImagePullPolicy" $drainPull
          "drainReplicas" $.Values.app.replicas
          "serviceAccountName" $.Values.serviceAccount.name
          "secretName" (include "vlm-worker.secretName" $)
          "resources" $.Values.resources
          "configData" $configData
          "releaseName" $.Release.Name
          "releaseNamespace" $.Release.Namespace
        ) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- $targets | toJson -}}
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
