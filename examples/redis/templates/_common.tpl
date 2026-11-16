{{- define "redis.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- end -}}
