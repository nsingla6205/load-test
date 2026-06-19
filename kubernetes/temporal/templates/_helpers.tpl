{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "temporal.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "temporal.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "temporal.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create the name of the service account
*/}}
{{- define "temporal.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ default (include "temporal.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
{{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Define the service account as needed
*/}}
{{- define "temporal.serviceAccount" -}}
serviceAccountName: {{ include "temporal.serviceAccountName" . }}
{{- end -}}

{{/*
Create a default fully qualified component name from the full app name and a component name.
We truncate the full name at 63 - 1 (last dash) - len(component name) chars because some Kubernetes name fields are limited to this (by the DNS naming spec)
and we want to make sure that the component is included in the name.
*/}}
{{- define "temporal.componentname" -}}
{{- $global := index . 0 -}}
{{- $component := index . 1 | trimPrefix "-" -}}
{{- printf "%s-%s" (include "temporal.fullname" $global | trunc (sub 62 (len $component) | int) | trimSuffix "-" ) $component | trimSuffix "-" -}}
{{- end -}}

{{/*
Define the AppVersion
*/}}
{{- define "temporal.appVersion" -}}
{{- if .Chart.AppVersion -}}
{{ .Chart.AppVersion | replace "+" "_" | quote }}
{{- else -}}
{{ include "temporal.chart" $ }}
{{- end -}}
{{- end -}}

{{/*
Create the annotations for all resources
*/}}
{{- define "temporal.resourceAnnotations" -}}
{{- $global := index . 0 -}}
{{- $scope := index . 1 -}}
{{- $resourceType := index . 2 -}}
{{- $component := "server" -}}
{{- if (or (eq $scope "admintools") (eq $scope "web")) -}}
{{- $component = $scope -}}
{{- end -}}
{{- with $resourceType -}}
{{- $resourceTypeKey := printf "%sAnnotations" . -}}
{{- $componentAnnotations := (index $global.Values $component $resourceTypeKey) -}}
{{- $scopeAnnotations := dict -}}
{{- if hasKey (index $global.Values $component) $scope -}}
{{- $scopeAnnotations = (index $global.Values $component $scope $resourceTypeKey) -}}
{{- end -}}
{{- $resourceAnnotations := merge $scopeAnnotations $componentAnnotations -}}
{{- range $annotation_name, $annotation_value := $resourceAnnotations }}
{{ $annotation_name }}: {{ $annotation_value | quote }}
{{- end -}}
{{- end -}}
{{- range $annotation_name, $annotation_value := $global.Values.additionalAnnotations }}
{{ $annotation_name }}: {{ $annotation_value | quote }}
{{- end -}}
{{- end -}}

{{/*
Create the labels for all resources
*/}}
{{- define "temporal.resourceLabels" -}}
{{- $global := index . 0 -}}
{{- $scope := index . 1 -}}
{{- $resourceType := index . 2 -}}
{{- $component := "server" -}}
{{- if (or (eq $scope "admintools") (eq $scope "web")) -}}
{{- $component = $scope -}}
{{- end -}}
{{- with $scope -}}
app.kubernetes.io/component: {{ . }}
{{ end -}}
app.kubernetes.io/name: {{ include "temporal.name" $global }}
helm.sh/chart: {{ include "temporal.chart" $global }}
app.kubernetes.io/managed-by: {{ index $global "Release" "Service" }}
app.kubernetes.io/instance: {{ index $global "Release" "Name" }}
app.kubernetes.io/version: {{ include "temporal.appVersion" $global }}
app.kubernetes.io/part-of: {{ $global.Chart.Name }}
{{- with $resourceType -}}
{{- $resourceTypeKey := printf "%sLabels" . -}}
{{- $componentLabels := (index $global.Values $component $resourceTypeKey) -}}
{{- $scopeLabels := dict -}}
{{- if hasKey (index $global.Values $component) $scope -}}
{{- $scopeLabels = (index $global.Values $component $scope $resourceTypeKey) -}}
{{- end -}}
{{- $resourceLabels := merge $scopeLabels $componentLabels -}}
{{- range $label_name, $label_value := $resourceLabels }}
{{ $label_name}}: {{ $label_value | quote }}
{{- end -}}
{{- end -}}
{{- range $label_name, $label_value := $global.Values.additionalLabels }}
{{ $label_name }}: {{ $label_value | quote }}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.schema" -}}
{{- if eq . "default" -}}
{{- print "temporal" -}}
{{- else -}}
{{- print . -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.driver" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- required (printf "Persistence driver for %s store is not set or is not 'sql' (set server.config.persistence.%s.driver to 'sql')" $store $store) $storeConfig.driver -}}
{{- end -}}

{{- define "temporal.persistence.sql.database" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- if $storeConfig.sql.database -}}
{{- $storeConfig.sql.database -}}
{{- else -}}
{{- required (printf "Please specify database for %s store" $store) -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.driver" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- required (printf "Please specify sql driver for %s store (e.g. postgres12)" $store) $storeConfig.sql.driver -}}
{{- end -}}

{{- define "temporal.persistence.sql.host" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- if include "temporal.cloudSqlIamAuthEnabled" (list $global) | toString | eq "true" -}}
{{- "127.0.0.1" -}}
{{- else -}}
{{- required (printf "Please specify sql host for %s store" $store) $storeConfig.sql.host -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.port" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- required (printf "Please specify sql port for %s store" $store) $storeConfig.sql.port -}}
{{- end -}}

{{- define "temporal.persistence.sql.user" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- if include "temporal.cloudSqlIamAuthEnabled" (list $global) | toString | eq "true" -}}
{{- $serviceAccountName := "temporal-ksa" -}}
{{- $projectId := "" -}}
{{- if hasKey $global.Values "global" -}}
{{- if hasKey $global.Values.global "gcpProjectId" -}}
{{- $projectId = $global.Values.global.gcpProjectId -}}
{{- end -}}
{{- end -}}
{{- if eq $projectId "" -}}
{{- if hasKey $global.Values "gcpProjectId" -}}
{{- $projectId = $global.Values.gcpProjectId -}}
{{- end -}}
{{- end -}}
{{- if eq $projectId "" -}}
{{- required "gcpProjectId must be set when cloudSqlIamAuthEnabled is true. Set it in .Values.gcpProjectId or .Values.global.gcpProjectId" $projectId -}}
{{- end -}}
{{- printf "%s@%s.iam" $serviceAccountName $projectId -}}
{{- else -}}
{{- required (printf "Please specify sql user for %s store" $store) $storeConfig.sql.user -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.password" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- if $storeConfig.sql.password -}}
{{- $storeConfig.sql.password -}}
{{- else -}}
{{- required (printf "Please specify sql password for %s store (or existingSecret to use external secret)" $store) $storeConfig.sql.password -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.secretName" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- $driverConfig := $storeConfig.sql -}}
{{- if $driverConfig.existingSecret -}}
{{- $driverConfig.existingSecret -}}
{{- else if $driverConfig.secretName -}}
{{- print $driverConfig.secretName -}}
{{- else if $storeConfig.sql.password -}}
{{- include "temporal.componentname" (list $global (printf "%s-store" $store)) -}}
{{- else -}}
{{- required (printf "Please specify sql password or existingSecret for %s store" $store) $storeConfig.sql.existingSecret -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.secretKey" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- $driverConfig := $storeConfig.sql -}}
{{- if $driverConfig.secretKey -}}
{{- print $driverConfig.secretKey -}}
{{- else if or $driverConfig.existingSecret $driverConfig.password -}}
{{- print "password" -}}
{{- else -}}
{{- fail (printf "Please specify sql password or existingSecret for %s store" $store) -}}
{{- end -}}
{{- end -}}

{{- define "temporal.persistence.sql.connectAttributes" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- $storeConfig := index $global.Values.server.config.persistence $store -}}
{{- $driverConfig := $storeConfig.sql -}}
{{- $result := list -}}
{{- range $key, $value := $driverConfig.connectAttributes -}}
  {{- $result = append $result (printf "%s=%v" $key $value) -}}
{{- end -}}
{{- join "&" $result -}}
{{- end -}}

{{- define "temporal.persistence.secretName" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- include (printf "temporal.persistence.%s.secretName" (include "temporal.persistence.driver" (list $global $store))) (list $global $store) -}}
{{- end -}}

{{- define "temporal.persistence.secretKey" -}}
{{- $global := index . 0 -}}
{{- $store := index . 1 -}}
{{- include (printf "temporal.persistence.%s.secretKey" (include "temporal.persistence.driver" (list $global $store))) (list $global $store) -}}
{{- end -}}

{{/*
Based on Bitnami charts method
Renders a value that contains template.
Usage:
{{ include "common.tplvalues.render" ( dict "value" .Values.path.to.the.Value "context" $) }}
*/}}
{{- define "common.tplvalues.render" -}}
    {{- if typeIs "string" .value }}
        {{- tpl .value .context }}
    {{- else }}
        {{- tpl (.value | toYaml) .context }}
    {{- end }}
{{- end -}}

{{/*
To modify camelCase to hyphenated internal-frontend service name
*/}}
{{- define "serviceName" -}}
    {{- $service := index . 0 -}}
    {{- if eq $service "internalFrontend" }}
        {{- print "internal-frontend" }}
    {{- else }}
        {{- print $service }}
    {{- end }}
{{- end -}}

{{/*
Helper function to check if IAM authentication is enabled
Checks .Values.global.cloudSqlIamAuthEnabled first (for umbrella charts), then .Values.cloudSqlIamAuthEnabled
*/}}
{{- define "temporal.cloudSqlIamAuthEnabled" -}}
{{- $global := index . 0 -}}
{{- $iamAuthEnabled := false -}}
{{- if hasKey $global.Values "global" -}}
{{- if hasKey $global.Values.global "cloudSqlIamAuthEnabled" -}}
{{- $iamAuthEnabled = $global.Values.global.cloudSqlIamAuthEnabled -}}
{{- end -}}
{{- end -}}
{{- if not $iamAuthEnabled -}}
{{- if hasKey $global.Values "cloudSqlIamAuthEnabled" -}}
{{- $iamAuthEnabled = $global.Values.cloudSqlIamAuthEnabled -}}
{{- end -}}
{{- end -}}
{{- $iamAuthEnabled -}}
{{- end -}}

{{/*
Helper function to conditionally include Cloud SQL Proxy sidecar container.
Only included when cloudSqlIamAuthEnabled is true.

For server Deployments (default, probes=true): bare proxy with no probes and no
shutdown flags — matches the VCP worker pattern. The proxy starts in <1s and the
Temporal server retries DB connections naturally. No probes avoids ASM/Istio
probe-rewriting conflicts.

For schema Jobs (probes=false): adds --quitquitquit and --admin-port so the
main container can shut the proxy down after the job finishes.
*/}}
{{- define "temporal.databaseProxyContainer" -}}
{{- $root := index . 0 -}}
{{- $opts := dict -}}
{{- if ge (len .) 2 -}}
{{- $opts = index . 1 -}}
{{- end -}}
{{- $enableProbes := true -}}
{{- if hasKey $opts "probes" -}}
{{- $enableProbes = $opts.probes -}}
{{- end -}}
{{- if include "temporal.cloudSqlIamAuthEnabled" (list $root) | toString | eq "true" }}
{{- $instanceConnectionName := $root.Values.cloudSqlInstanceConnectionName }}
{{- if eq $instanceConnectionName "" }}
{{- $project := "" }}
{{- if hasKey $root.Values "global" -}}
{{- if hasKey $root.Values.global "gcpProjectId" -}}
{{- $project = $root.Values.global.gcpProjectId -}}
{{- end -}}
{{- end -}}
{{- if eq $project "" -}}
{{- if hasKey $root.Values "gcpProjectId" -}}
{{- $project = $root.Values.gcpProjectId -}}
{{- end -}}
{{- end -}}
{{- if eq $project "" -}}
{{- required "gcpProjectId must be set when cloudSqlIamAuthEnabled is true. Set it in .Values.gcpProjectId or .Values.global.gcpProjectId" $project -}}
{{- end -}}
{{- $region := "australia-southeast1" -}}
{{- if hasKey $root.Values "global" -}}
{{- if and (hasKey $root.Values.global "gcpRegion") (ne $root.Values.global.gcpRegion "") -}}
{{- $region = $root.Values.global.gcpRegion -}}
{{- end -}}
{{- end -}}
{{- if and (hasKey $root.Values "gcpRegion") (ne $root.Values.gcpRegion "") -}}
{{- $region = $root.Values.gcpRegion -}}
{{- end -}}
{{- $instance := printf "%s-db-postgres" $project }}
{{- $instanceConnectionName = printf "%s:%s:%s" $project $region $instance }}
{{- end }}
{{- $img := "" -}}
{{- if hasKey $root.Values "global" -}}
{{- if hasKey $root.Values.global "cloudSqlProxy" -}}
{{- $img = $root.Values.global.cloudSqlProxy.image -}}
{{- end -}}
{{- end -}}
{{- if eq $img "" -}}
{{- if hasKey $root.Values "cloudSqlProxy" -}}
{{- $img = $root.Values.cloudSqlProxy.image -}}
{{- end -}}
{{- end -}}
{{- if eq $img "" -}}
{{- $img = "gcr.io/cloud-sql-connectors/cloud-sql-proxy:2.21.1" -}}
{{- end }}
- name: cloud-sql-proxy
  image: {{ $img | quote }}
  args:
    - "--private-ip"
    - "--auto-iam-authn"
    - "--structured-logs"
    {{- if not $enableProbes }}
    - "--quitquitquit"
    - "--admin-port=9091"
    {{- end }}
    - "--port=5432"
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