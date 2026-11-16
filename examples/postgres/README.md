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
| `network.port` | `5432` | Host port |
| `storage.enabled` | `true` | Keep the data directory on a volume (retained on uninstall) |
