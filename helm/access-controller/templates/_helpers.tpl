{{/*
Expand the name of the chart.
*/}}
{{- define "access-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "access-controller.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "access-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "access-controller.labels" -}}
helm.sh/chart: {{ include "access-controller.chart" . }}
{{ include "access-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "access-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "access-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "access-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "access-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "access-controller.joinAddresses" -}}
{{- range $i, $_ := until (.Values.statefulset.replicas | int) -}}
    {{- if gt $i 0 -}},{{- end -}}
    {{ include "access-controller.fullname" $ }}-{{ $i }}.{{ include "access-controller.fullname" $ }}.{{ $.Release.Namespace }}.svc.{{ $.Values.clusterDomain }}:{{ $.Values.service.ports.gossip.port | int64 -}}
{{- end -}}
{{- end -}}