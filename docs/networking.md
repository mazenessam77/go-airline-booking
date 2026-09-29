# Private networking and interface bindings

[Back to the lab](../README.md) · [Monitoring setup](monitoring/prometheus-grafana.md)

The Mac's ordinary LAN address, such as `192.168.x.x`, is private to its local network. An AWS EC2 instance cannot route to it directly across the public internet. In the reported monitoring experiment, both machines joined a Tailscale private overlay network:

```text
EC2: 100.x.x.x  ---- Tailscale private path ----  Mac: 100.x.x.x
     Prometheus                                Go API :8080
```

In the original lab the Mac's API was reachable at its Tailscale address on port 8080 (`100.x.y.z:8080`). Use your own Mac's Tailscale IPv4 address. The EC2 host (and the Prometheus pod, if scraping directly) must be able to reach it through your private network policy. Do not expose the development machine on a public interface for this lab.

## The “ping works, curl fails” incident

The original Compose port mapping was `127.0.0.1:8080:8080`. The API was listening through Docker only on the Mac's loopback interface. EC2 could reach the Mac's Tailscale address with a network test, but a TCP connection to port 8080 was refused. The path existed; nothing accepted that port on the Tailscale interface.

The checked-in [Compose file](../docker-compose.yml) now accepts `API_BIND_ADDR` and defaults to `127.0.0.1`. In your ignored `.env`, set it to your Mac's current Tailscale IPv4 address when using remote Prometheus:

```dotenv
API_BIND_ADDR=YOUR_MAC_TAILSCALE_IP
```

Then recreate the API container and inspect the published address:

```sh
docker compose --profile app up -d --force-recreate api
docker compose ps api
lsof -nP -iTCP:8080 -sTCP:LISTEN
curl --fail-with-body "http://YOUR_MAC_TAILSCALE_IP:8080/metrics"
```

`lsof` can show Docker Desktop's host-side listener. The API itself listens on `:8080` inside the container. If `lsof` shows only `127.0.0.1:8080`, Prometheus cannot use the Tailscale address. If the Tailscale IP changes, update `.env`, recreate the container, and update the Kubernetes endpoint.

From EC2, test the exact HTTP target before changing Prometheus:

```sh
curl -v --connect-timeout 5 "http://YOUR_MAC_TAILSCALE_IP:8080/metrics"
```

A successful ping shows basic reachability, but it does not prove the application listens on the right interface and port. A failed `curl` can also come from a local firewall, Tailscale policy, a stopped container, or a missing route from the Prometheus pod. Test from the same network namespace as the scraper if the EC2 host succeeds but the Prometheus target fails.

## Binding only to the Tailscale address has two side effects

A published port listens on exactly one host address. Setting `API_BIND_ADDR` to the Tailscale IP fixes the remote scrape, and two more things change with it. Both were observed during this lab:

1. **Local requests to localhost stop working.** `curl http://127.0.0.1:8080/readyz` on the Mac fails with `Connection refused`, and so does anything else pointed at localhost. Use `http://YOUR_MAC_TAILSCALE_IP:8080` from the Mac too, or switch back to `127.0.0.1` when you don't need remote monitoring.
2. **The API can't start while Tailscale is down.** When Tailscale is stopped, its address no longer exists on the Mac, and Docker refuses the mapping:

   ```text
   Error response from daemon: ports are not available: exposing port TCP YOUR_MAC_TAILSCALE_IP:8080 -> 127.0.0.1:0:
   listen tcp4 YOUR_MAC_TAILSCALE_IP:8080: bind: can't assign requested address
   ```

   The container sits in `Created`. Start Tailscale, or override for one run without editing `.env`:

   ```sh
   API_BIND_ADDR=127.0.0.1 docker compose --profile app up -d api
   ```

Shell variables take precedence over `.env` in Compose, which is also how `LAB_FAULTS=...` works in the lab commands.

PostgreSQL remains published as `127.0.0.1:5433:5432` in Compose. The remote monitor needs only `/metrics` on the API; there is no reason to publish PostgreSQL through Tailscale for this lab. See the [host vs container database address](database-troubleshooting.md#first-connect-to-the-right-database) lesson.
