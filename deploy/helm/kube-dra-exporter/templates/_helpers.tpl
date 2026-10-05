{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "kube-dra-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
*/}}
{{- define "kube-dra-exporter.fullname" -}}
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
Create the name of the service account to use
*/}}
{{- define "kube-dra-exporter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{ default (include "kube-dra-exporter.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "chartName" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Labels that should be added on each resource
*/}}
{{- define "labels" -}}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- if eq .Values.creator "helm" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ include "chartName" . }}
{{- end -}}
{{- if .Values.global.commonLabels}}
{{ toYaml .Values.global.commonLabels }}
{{- end }}
{{- end -}}

{{/*
Namespace for all resources to be installed into
If not defined in values file then the helm release namespace is used
By default this is not set so the helm release namespace will be used

This gets around an problem within helm discussed here
https://github.com/helm/helm/issues/5358
*/}}
{{- define "kube-dra-exporter.namespace" -}}
    {{ .Values.namespace | default .Release.Namespace }}
{{- end -}}

{{/*
Util function for generating the image URL based on the provided options.
*/}}
{{- define "kube-dra-exporter.image" -}}
{{- /*
Calling convention:

- (tuple <imageValues> <imageRegistry> <imageNamespace> <defaultReference>)

We intentionally pass imageRegistry/imageNamespace as explicit arguments rather than reading
from `.Values` inside this helper, because `helm-tool lint` does not reliably track `.Values.*`
usage through tuple/variable indirection.
*/ -}}

{{- if ne (len .) 4 -}}
    {{- fail (printf "ERROR: template \"kube-dra-exporter.image\" expects (tuple <imageValues> <imageRegistry> <imageNamespace> <defaultReference>), got %d arguments" (len .)) -}}
{{- end -}}

{{- $image := index . 0 -}}
{{- $imageRegistry := index . 1 | default "" -}}
{{- $imageNamespace := index . 2 | default "" -}}
{{- $defaultReference := index . 3 -}}

{{- $repository := "" -}}
{{- if $image.repository -}}
    {{- $repository = $image.repository -}}

{{- else -}}
    {{- $name := required "ERROR: image.name must be set when image.repository is empty" $image.name -}}
    {{- $repository = $name -}}

    {{- if $imageNamespace -}}
        {{- $repository = printf "%s/%s" $imageNamespace $repository -}}
    {{- end -}}

    {{- if $imageRegistry -}}
        {{- $repository = printf "%s/%s" $imageRegistry $repository -}}
    {{- end -}}

{{- end -}}

{{- $repository -}}
{{- if and $image.tag $image.digest -}}
    {{- printf ":%s@%s" $image.tag $image.digest -}}
{{- else if $image.tag -}}
    {{- printf ":%s" $image.tag -}}
{{- else if $image.digest -}}
    {{- printf "@%s" $image.digest -}}
{{- else -}}
    {{- printf "%s" $defaultReference -}}
{{- end -}}
{{- end }}
