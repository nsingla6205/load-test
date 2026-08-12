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
{{- $imageValueName := index $args "name" -}}
{{- $imageConfig := index $context.Values.images $imageValueName -}}
{{- $imageName := $imageConfig.name -}}
{{- $imageTag := $imageConfig.tag -}}
{{- $imageDigest := index $args "sha" -}}
{{- $isSecondary := index $args "secondary" -}}
{{- $imageVersion := index $args "version" -}}
{{- $forceSecondary := default false $context.Values.global.useSecondaryImageRegistry -}}
{{- $useSecondary := or $isSecondary $forceSecondary -}}
{{- $secondaryRegistry := include "secondImageRegistryFullPath" $context | trim -}}
{{- $registry := include "imageRegistryFullPath" $context | trim -}}
{{- if and $useSecondary (ne $secondaryRegistry "") -}}
{{- $registry = $secondaryRegistry -}}
{{- end -}}
{{- if $context.Values.global.useTags -}}
{{- /* Prefer per-entry version name from versionsSupported[]; fall back to images.<name>.tag for legacy single-version installs. */ -}}
{{- $finaltag := toString ($imageVersion | default $imageTag) -}}
{{- printf "%s/%s:%s" $registry $imageName $finaltag -}}
{{- else -}}
{{- /* Prefer per-entry sha from versionsSupported[]; fall back to images.<name>.digest.
       Strip a leading "sha256:" so hand-edited overrides pasted straight from
       `docker inspect` don't produce invalid @sha256:sha256:... refs.
       Fail fast when both are empty so we never render an invalid @sha256: ref. */ -}}
{{- $finalDigest := toString ($imageDigest | default $imageConfig.digest) -}}
{{- $finalDigest = trimPrefix "sha256:" $finalDigest -}}
{{- $finalDigest = required (printf "vcp-worker: image digest required when global.useTags=false — set versionsSupported[].sha or images.%s.digest" $imageValueName) $finalDigest -}}
{{- printf "%s/%s@sha256:%s" $registry $imageName $finalDigest -}}
{{- end -}}
{{- end -}}

{{/*
Helper function to generate the configMap name by appending "-config" to the app name.
*/}}
{{- define "vcp-worker.configMapName" -}}
{{- printf "%s-config" . -}}
{{- end -}}

{{/* Helper function to convert a string to upper snake case.
   Example: "myVariableName" becomes "MY_VARIABLE_NAME"
*/}}
{{- define "toUpperSnakeCase" -}}
{{- $camel := . -}}
{{- $snake := regexReplaceAll "([a-z])([A-Z])" $camel "${1}_${2}" -}}
{{- upper $snake -}}
{{- end -}}

{{/*
Convert a version name (e.g. "26063.0.0-OCI-RC.1", "01042026_02") into a
DNS-1123 compliant slug safe for Kubernetes resource names and labels.
Lowercases, replaces dots and underscores with hyphens, trims leading/trailing
hyphens so tags like "build_tag_" don't produce a trailing dash.
*/}}
{{- define "vcp-worker.versionSlug" -}}
{{- . | lower | replace "." "-" | replace "_" "-" | trimPrefix "-" | trimSuffix "-" -}}
{{- end -}}

{{/*
Fail-fast guard: versionsSupported[] must contain at least one entry.
An empty/nil array would silently produce zero Deployments and ConfigMaps,
leaving the release without any worker pods. CI stamps the default placeholder
to real build tags at release time (see .github/scripts/fileToFieldsMapping.json);
override files must also set versionsSupported[] explicitly (see Section 12a in
doc/guides/oci-deploy-templates/VCP_OCI_DEPLOY_GUIDE.md).
Templates that iterate versionsSupported MUST call this first.
*/}}
{{- define "vcp-worker.validateVersions" -}}
{{- if not .Values.versionsSupported -}}
{{- fail "vcp-worker: .Values.versionsSupported must contain at least one entry (each entry renders a Deployment + ConfigMap and drives TASK_QUEUE_VERSION). See doc/architecture/designs/0011-vcp-worker_versioning-design.md." -}}
{{- end -}}
{{- end -}}