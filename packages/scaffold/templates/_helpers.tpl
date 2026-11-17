{{/* The address the service answers on, for NOTES.txt. */}}
{{- define "url" -}}
{{- if .Values.route.host -}}
http://{{ .Values.route.host }}/
{{- else -}}
http://{{ .Values.service.hostIP }}:{{ .Values.service.port }}/
{{- end -}}
{{- end -}}
