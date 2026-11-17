# redis

Single Redis instance with persistent storage.

```bash
karga template my-redis examples/redis --set image.tag=7.4.2
```

| Value | Default | Description |
|-------|---------|-------------|
| `image.tag` | `7.4` | Redis image tag |
| `service.enabled` | `true` | A Service `redis` on the release network, how other workloads reach Redis |
| `service.port` | `6379` | Service port |
| `service.listener` | — | Expose Redis on this tcp gateway listener (the host's administrator adds it) |
| `storage.enabled` | `true` | Keep `/data` on a volume (retained on uninstall) |
| `config.maxmemory` | `256mb` | `maxmemory` in redis.conf |
