{{/*
Expand the name of the chart.
*/}}
{{- define "openserve.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "openserve.fullname" -}}
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

{{/*
Chart label.
*/}}
{{- define "openserve.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "openserve.labels" -}}
helm.sh/chart: {{ include "openserve.chart" . }}
{{ include "openserve.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels (stable subset used in matchLabels — never add fields here without
a deployment restart strategy, as changes break rolling updates).
*/}}
{{- define "openserve.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openserve.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Image tag helper — defaults to Chart.AppVersion if tag is not set.
Usage: {{ include "openserve.imageTag" (dict "image" .Values.operator.image "chart" .Chart) }}
*/}}
{{- define "openserve.imageTag" -}}
{{- if .image.tag }}
{{- .image.tag }}
{{- else }}
{{- .chart.AppVersion }}
{{- end }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "openserve.serviceAccountName" -}}
{{- include "openserve.fullname" . }}
{{- end }}

{{/*
Operator image reference.
*/}}
{{- define "openserve.operatorImage" -}}
{{ .Values.operator.image.repository }}:{{ include "openserve.imageTag" (dict "image" .Values.operator.image "chart" .Chart) }}
{{- end }}

{{/*
Control API image reference.
*/}}
{{- define "openserve.controlApiImage" -}}
{{ .Values.controlApi.image.repository }}:{{ include "openserve.imageTag" (dict "image" .Values.controlApi.image "chart" .Chart) }}
{{- end }}

{{/*
Gateway image reference.
*/}}
{{- define "openserve.gatewayImage" -}}
{{ .Values.gateway.image.repository }}:{{ include "openserve.imageTag" (dict "image" .Values.gateway.image "chart" .Chart) }}
{{- end }}

{{/*
GUI image reference.
*/}}
{{- define "openserve.guiImage" -}}
{{ .Values.gui.image.repository }}:{{ include "openserve.imageTag" (dict "image" .Values.gui.image "chart" .Chart) }}
{{- end }}

{{/*
Validate required values. Add to each template that needs them.
*/}}
{{- define "openserve.validateRequired" -}}
{{- required "domain is required (e.g. ai.acme.com)" .Values.domain | quote }}
{{- required "google.clientId is required" .Values.google.clientId | quote }}
{{- required "postgres.host is required" .Values.postgres.host | quote }}
{{- required "redis.host is required" .Values.redis.host | quote }}
{{- required "gcs.bucket is required" .Values.gcs.bucket | quote }}
{{- end }}
