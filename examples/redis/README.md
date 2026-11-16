# redis

Single Redis instance with persistent storage.

```bash
karga template my-redis examples/redis --set image.tag=7.4.2
```

| Value | Default | Description |
|-------|---------|-------------|
| `image.tag` | `7.4` | Redis image tag |
| `network.enabled` | `true` | Publish Redis on the host and create a network |
| `network.port` | `6379` | Host port |
| `storage.enabled` | `true` | Keep `/data` on a volume (retained on uninstall) |
| `config.maxmemory` | `256mb` | `maxmemory` in redis.conf |
