{{/*
Expand the name of the chart.
*/}}
{{- define "skopos.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "skopos.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "skopos.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.labels" -}}
helm.sh/chart: {{ include "skopos.chart" . }}
app.kubernetes.io/name: {{ include "skopos.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "skopos.selectorLabels" -}}
app.kubernetes.io/name: {{ include "skopos.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "skopos.componentLabels" -}}
{{- $root := .root -}}
{{- $component := .component -}}
{{ include "skopos.selectorLabels" $root }}
app.kubernetes.io/component: {{ $component }}
{{- end }}

{{- define "skopos.topologySpread" -}}
{{- if .constraints -}}
{{- toYaml .constraints -}}
{{- else if gt (int .replicas) 1 -}}
- maxSkew: 1
  topologyKey: kubernetes.io/hostname
  whenUnsatisfiable: ScheduleAnyway
  labelSelector:
    matchLabels:
      {{- include "skopos.componentLabels" (dict "root" .root "component" .component) | nindent 6 }}
{{- end -}}
{{- end }}

{{- define "skopos.clickhouse.fullname" -}}
{{- printf "%s-clickhouse" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.collector.fullname" -}}
{{- printf "%s-collector" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.collector.headlessName" -}}
{{- printf "%s-headless" (include "skopos.collector.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.checkpoint.fullname" -}}
{{- printf "%s-checkpoint" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.checkpoint.secretName" -}}
{{- if .Values.checkpoint.s3.existingSecret }}
{{- .Values.checkpoint.s3.existingSecret }}
{{- else }}
{{- printf "%s-checkpoint-s3" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.obi.fullname" -}}
{{- printf "%s-obi" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.gateway.fullname" -}}
{{- printf "%s-gateway" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.frontend.fullname" -}}
{{- printf "%s-frontend" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.query.fullname" -}}
{{- printf "%s-query" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.clickhouseInit.fullname" -}}
{{- printf "%s-clickhouse-init" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.clickhouse.secretName" -}}
{{- if .Values.clickhouse.auth.existingSecret }}
{{- .Values.clickhouse.auth.existingSecret }}
{{- else }}
{{- printf "%s-clickhouse-credentials" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.auth.secretName" -}}
{{- if .Values.auth.existingSecret }}
{{- .Values.auth.existingSecret }}
{{- else }}
{{- printf "%s-auth" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.context.configMapName" -}}
{{- if .Values.context.existingConfigMap }}
{{- .Values.context.existingConfigMap }}
{{- else }}
{{- printf "%s-context" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.clickhouse.host" -}}
{{- include "skopos.clickhouse.fullname" . }}
{{- end }}

{{- define "skopos.clickhouse.storageSecretName" -}}
{{- if .Values.clickhouse.storage.cold.existingSecret }}
{{- .Values.clickhouse.storage.cold.existingSecret }}
{{- else }}
{{- printf "%s-clickhouse-s3" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.clickhouse.backupSecretName" -}}
{{- if .Values.clickhouse.backup.existingSecret }}
{{- .Values.clickhouse.backup.existingSecret }}
{{- else }}
{{- printf "%s-clickhouse-backup-s3" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "skopos.clickhouseBackup.fullname" -}}
{{- printf "%s-clickhouse-backup" (include "skopos.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.collector.host" -}}
{{- include "skopos.collector.fullname" . }}
{{- end }}

{{- define "skopos.gateway.host" -}}
{{- include "skopos.gateway.fullname" . }}
{{- end }}

{{/*
Render an image reference with an optional digest.
Usage: include "skopos.image" (dict "image" .Values.clickhouse.image)
*/}}
{{- define "skopos.image" -}}
{{- $image := .image -}}
{{- if $image.digest -}}
{{- printf "%s:%s@%s" $image.repository $image.tag $image.digest -}}
{{- else -}}
{{- printf "%s:%s" $image.repository $image.tag -}}
{{- end -}}
{{- end }}

{{- define "skopos.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "skopos.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "skopos.obi.serviceAccountName" -}}
{{- if .Values.obi.serviceAccount.name }}
{{- .Values.obi.serviceAccount.name }}
{{- else if .Values.obi.serviceAccount.create }}
{{- include "skopos.obi.fullname" . }}
{{- else }}
{{- include "skopos.serviceAccountName" . }}
{{- end }}
{{- end }}

{{- define "skopos.obi.clusterRoleName" -}}
{{- printf "%s-%s-obi" (include "skopos.fullname" .) .Release.Namespace | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skopos.validateAdmission" -}}
{{- $max := int .Values.gateway.config.maxInflight -}}
{{- $depth := int .Values.gateway.config.queueDepth -}}
{{- $wait := int .Values.gateway.config.queueWaitSeconds -}}
{{- if lt $max 0 -}}
{{- fail "gateway.config.maxInflight must not be negative" -}}
{{- end -}}
{{- if lt $depth 0 -}}
{{- fail "gateway.config.queueDepth must not be negative" -}}
{{- end -}}
{{- if lt $wait 0 -}}
{{- fail "gateway.config.queueWaitSeconds must not be negative" -}}
{{- end -}}
{{- if and (eq $max 0) (or (gt $depth 0) (gt $wait 0)) -}}
{{- fail "gateway.config.queueDepth and gateway.config.queueWaitSeconds require gateway.config.maxInflight" -}}
{{- end -}}
{{- if and (gt $depth 0) (eq $wait 0) -}}
{{- fail "gateway.config.queueWaitSeconds must be positive when gateway.config.queueDepth is set" -}}
{{- end -}}
{{- if and (eq $depth 0) (gt $wait 0) -}}
{{- fail "gateway.config.queueDepth must be positive when gateway.config.queueWaitSeconds is set" -}}
{{- end -}}
{{- end -}}

{{- define "skopos.memoryBytes" -}}
{{- $value := toString . -}}
{{- $units := dict "Ki" 1024 "Mi" 1048576 "Gi" 1073741824 "K" 1000 "M" 1000000 "G" 1000000000 "B" 1 -}}
{{- $unit := "" -}}
{{- range $suffix, $_ := $units -}}
{{- if hasSuffix $suffix $value -}}{{- $unit = $suffix -}}{{- end -}}
{{- end -}}
{{- $number := $value -}}
{{- if $unit -}}{{- $number = trimSuffix $unit $value -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $number) -}}
{{- fail (printf "invalid memory quantity %q" $value) -}}
{{- end -}}
{{- $multiplier := 1 -}}
{{- if $unit -}}{{- $multiplier = index $units $unit -}}{{- end -}}
{{- int64 (mulf (float64 $number) (float64 $multiplier)) -}}
{{- end -}}

{{- define "skopos.goMemLimit" -}}
{{- if .Values.gateway.goMemLimit -}}
{{- include "skopos.memoryBytes" .Values.gateway.goMemLimit -}}
{{- else -}}
{{- with .Values.gateway.resources -}}
{{- with .limits -}}
{{- with .memory -}}
{{- $bytes := include "skopos.memoryBytes" . | int64 -}}
{{- int64 (mulf (float64 $bytes) 0.9) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
