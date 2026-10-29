{{- define "rvav.name" -}}{{ .Release.Name }}{{- end }}

{{- define "rvav.labels" -}}
app.kubernetes.io/name: rvav
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{- define "rvav.selector" -}}
app.kubernetes.io/name: rvav
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "rvav.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "rvav.secretName" -}}
{{ .Values.secret.existingSecret | default (printf "%s-env" .Release.Name) }}
{{- end }}

{{- define "rvav.serviceAccount" -}}
{{ if .Values.serviceAccount.create }}{{ .Values.serviceAccount.name | default .Release.Name }}{{ else }}{{ .Values.serviceAccount.name | default "default" }}{{ end }}
{{- end }}

{{/* Settings every rvav process reads: the ConfigMap, then the Secret. */}}
{{- define "rvav.envFrom" -}}
- configMapRef: { name: {{ .Release.Name }}-config }
- secretRef: { name: {{ include "rvav.secretName" . }} }
{{- end }}

{{/* Replicas on different nodes when they can be. */}}
{{- define "rvav.spread" -}}
{{- if .root.Values.spreadAcrossNodes }}
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway
    labelSelector:
      matchLabels:
        {{- include "rvav.selector" . | nindent 8 }}
{{- end }}
{{- end }}

{{/* An rvav process: api, worker or sync. */}}
{{- define "rvav.deployment" -}}
{{- $v := index .root.Values .component -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .root.Release.Name }}-{{ .component }}
  labels:
    {{- include "rvav.labels" .root | nindent 4 }}
    app.kubernetes.io/component: {{ .component }}
spec:
  replicas: {{ $v.replicas }}
  selector:
    matchLabels:
      {{- include "rvav.selector" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "rvav.labels" .root | nindent 8 }}
        app.kubernetes.io/component: {{ .component }}
      annotations:
        # Restarted when a setting changes.
        checksum/config: {{ include (print .root.Template.BasePath "/config.yaml") .root | sha256sum }}
    spec:
      serviceAccountName: {{ include "rvav.serviceAccount" .root }}
      {{- with .root.Values.imagePullSecrets }}
      imagePullSecrets: {{ toYaml . | nindent 8 }}
      {{- end }}
      # The image's user (distroless nonroot), by number.
      securityContext: {{ toYaml (merge (dict "runAsUser" 65532 "runAsGroup" 65532) .root.Values.podSecurityContext) | nindent 8 }}
      {{- include "rvav.spread" . | nindent 6 }}
      {{- with .root.Values.nodeSelector }}
      nodeSelector: {{ toYaml . | nindent 8 }}
      {{- end }}
      {{- with .root.Values.tolerations }}
      tolerations: {{ toYaml . | nindent 8 }}
      {{- end }}
      initContainers:
        - name: migrate
          image: {{ include "rvav.image" .root }}
          imagePullPolicy: {{ .root.Values.image.pullPolicy }}
          args: ["migrate"{{ if and .root.Values.migrate.seed (eq .component "api") }}, "-seed"{{ end }}]
          envFrom: {{ include "rvav.envFrom" .root | nindent 12 }}
          securityContext: {{ toYaml .root.Values.securityContext | nindent 12 }}
      containers:
        - name: {{ .component }}
          image: {{ include "rvav.image" .root }}
          imagePullPolicy: {{ .root.Values.image.pullPolicy }}
          args: [{{ .component | quote }}]
          envFrom: {{ include "rvav.envFrom" .root | nindent 12 }}
          ports:
            - { name: http, containerPort: 8080 }
          {{- if eq .component "api" }}
          readinessProbe:
            httpGet: { path: /readyz, port: http }
            periodSeconds: 5
          livenessProbe:
            httpGet: { path: /healthz, port: http }
            periodSeconds: 10
            failureThreshold: 6
          {{- end }}
          resources: {{ toYaml $v.resources | nindent 12 }}
          securityContext: {{ toYaml .root.Values.securityContext | nindent 12 }}
{{- end }}
