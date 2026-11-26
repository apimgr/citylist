package swagger

// ThemeCSS styles Swagger UI using the shared page palette variables.
func ThemeCSS() string {
	return `
#swagger-ui {
  background: var(--color-bg-primary);
  color: var(--color-text-primary);
}
#swagger-ui .topbar {
  background: var(--color-bg-secondary);
}
#swagger-ui .info .title,
#swagger-ui .opblock-tag {
  color: var(--color-text-primary);
}
#swagger-ui .opblock.opblock-get {
  background: color-mix(in srgb, var(--color-info) 10%, transparent);
  border-color: var(--color-info);
}
#swagger-ui .opblock.opblock-post {
  background: color-mix(in srgb, var(--color-success) 10%, transparent);
  border-color: var(--color-success);
}
#swagger-ui .opblock.opblock-put {
  background: color-mix(in srgb, var(--color-warning) 10%, transparent);
  border-color: var(--color-warning);
}
#swagger-ui .opblock.opblock-delete {
  background: color-mix(in srgb, var(--color-error) 10%, transparent);
  border-color: var(--color-error);
}
#swagger-ui input,
#swagger-ui textarea,
#swagger-ui select {
  background: var(--color-bg-primary);
  color: var(--color-text-primary);
  border: 1px solid var(--color-border);
}
#swagger-ui .btn {
  background: var(--color-primary);
  color: var(--color-on-error);
}
`
}
