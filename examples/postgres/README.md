# postgres

Single PostgreSQL instance. The password is passed as a Secret file
(`POSTGRES_PASSWORD_FILE`), never as an environment variable.

```bash
karga template my-db examples/postgres --set database.password=change-me
```

| Value | Default | Description |
|-------|---------|-------------|
| `database.password` | — | **Required.** Superuser password |
| `database.name` | `app` | Database created on first start |
| `database.user` | `app` | Superuser name |
| `service.enabled` | `true` | A Service `postgres` on the release network, how other workloads reach PostgreSQL |
| `service.port` | `5432` | Service port |
| `service.listener` | — | Expose PostgreSQL on this tcp gateway listener (the host's administrator adds it) |
| `storage.enabled` | `true` | Keep the data directory on a volume (retained on uninstall) |
