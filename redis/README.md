# the-M Redis

Source folder for the `them-redis` container configuration.

## Structure

```
redis/
├── config/
│   └── redis.conf    AOF persistence, memory limits, bind config
└── README.md
```

## Notes

- Data lives in `../data/them-redis/` — survives container rebuilds
- DB index **0** — the-M owns this Redis instance entirely (no shared use)
- All keys prefixed with `them:` or `rl:them:`

## Key namespaces

| Prefix | Purpose |
|---|---|
| `them:session:token:*` | L2 token cache, TTL 300s |
| `them:agents:registry` | Agent config cache |
| `them:agents:changed` | Pub/sub invalidation channel |
| `them:orchestrators:*` | Orchestrator config cache, TTL 600s |
| `them:orchestrators:changed` | Pub/sub invalidation channel |
| `them:dash:*` | Dashboard WebSocket broadcast |
| `them:bridge:*:heartbeat` | Replica heartbeat, TTL 30s |
| `rl:them:*` | Rate limiter INCR counters |

See `docs/reference/redis-generated.md` for full key space reference with TTLs and owners.
