{{/* The address the service answers on, for NOTES.txt. */}}
{{- define "url" -}}
{{- if .Values.expose.host -}}
http://{{ .Values.expose.host }}/ (gateway listener {{ .Values.expose.listener }})
{{- else -}}
http://web:{{ .Values.service.port }}/ from the release network (not exposed on the host)
{{- end -}}
{{- end -}}
