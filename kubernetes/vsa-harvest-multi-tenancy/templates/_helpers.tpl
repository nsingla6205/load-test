{{- define "vsa-harvest-multi-tenancy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "vsa-harvest-multi-tenancy.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "vsa-harvest-multi-tenancy.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "vsa-harvest-multi-tenancy.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride -}}
{{- end -}}

{{/*
Lowercase cluster name used for poller identity, TENANCY_CLUSTER_NAME, and
Service DNS (harvest-poller-<name>), matching gcnv-harvest + OTel scrape targets.
*/}}
{{- define "vsa-harvest-multi-tenancy.clusterName" -}}
{{- . | lower -}}
{{- end -}}

{{- define "vsa-harvest-multi-tenancy.labels" -}}
app.kubernetes.io/name: {{ include "vsa-harvest-multi-tenancy.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
harvest.netapp.com/cluster: {{ include "vsa-harvest-multi-tenancy.clusterName" .cluster.name | quote }}
{{- end -}}

{{- define "vsa-harvest-multi-tenancy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "vsa-harvest-multi-tenancy.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
