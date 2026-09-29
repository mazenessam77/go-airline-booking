# Incidents 4 and 5: when the environment is the problem

[All incidents](../lab.md) · [Back to the README](../../README.md) · [Networking](../networking.md) · [Database troubleshooting](../database-troubleshooting.md)

Both of these happened while building and monitoring this lab. No application code was wrong in either one. They're short, but they use the same method as the application incidents, and they come up constantly in real Docker and cloud work.

- **Observed:** what actually happened in this lab. Addresses are replaced with placeholders.
- **Code fact:** something you can read in this repository.

---

# Incident 4: "The API container keeps restarting"

## 1. Symptom

> "I changed the Compose config and rebuilt. Now the API won't come up."

## 2. What should I check first?

**Is the container running, and if not, how is it dying?** Don't start with the app. Start with what Docker says:

```sh
docker compose ps api
```

**Observed:** `Restarting (1) Less than a second ago`. The number in parentheses is the exit code. `1` means the process started and then exited with an error. It didn't get OOM-killed (usually `137`), and the image isn't broken (the container starts at all).

```sh
docker inspect --format 'restarts={{.RestartCount}} exit={{.State.ExitCode}}' "$(docker compose --profile app ps -aq api)"
```

**Observed:** `restarts=5 exit=1`, and climbing. `restart: unless-stopped` keeps retrying, so this is a crash loop.

## 3. Where should I look next?

**The container's own logs.** The process is exiting on purpose, so it probably said why:

```sh
docker compose logs --tail 5 api
```

**Observed:**

```json
{"level":"ERROR","msg":"application stopped","code":"startup_failed"}
```

That's all it says. **Code fact:** `cmd/api/main.go` logs only a fixed code when startup fails. It deliberately doesn't print the underlying error, because configuration errors can contain connection strings and passwords (see [security](../security.md)). So the log tells me *when* it fails (during startup, before serving HTTP) but not *why*.

When the logs won't tell me, I look at **what startup does** and **what configuration it received**.

## 4. What commands, metrics, and logs should I use?

**What startup does.** `run()` in `cmd/api/main.go` loads config, then calls `database.NewPostgresPool`, which **pings PostgreSQL** (`internal/database/postgres.go`) and returns an error if that fails. So "startup_failed right after starting" most likely means either bad config or an unreachable database.

**What config it received.** Print only the variable I need, with the password masked:

```sh
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$(docker compose --profile app ps -aq api)" \
  | grep '^DATABASE_URL=' | sed -E 's#//([^:]+):[^@]+@#//\1:****@#'
```

**Observed:**

```text
DATABASE_URL=postgres://USER:****@127.0.0.1:5433/airline_booking_demo?sslmode=disable
```

**Test that address from where the API runs**, meaning inside the Compose network, not from my laptop:

```sh
docker compose --profile app run --rm --no-deps --entrypoint sh api -c '
  nc -z -w 2 127.0.0.1 5433 && echo "127.0.0.1:5433 open" || echo "127.0.0.1:5433 refused"
  nc -z -w 2 postgres  5432 && echo "postgres:5432  open" || echo "postgres:5432  refused"'
```

## 5. What does each result mean?

**Observed:** `127.0.0.1:5433 refused`, `postgres:5432 open`.

```text
From the Mac:                         From inside the api container:
  127.0.0.1:5433 → PostgreSQL ✅         127.0.0.1 → the api container itself ❌
                                        postgres:5432 → PostgreSQL container ✅
```

`127.0.0.1` means "this same machine", and inside a container, that machine is **the container itself**. Nothing listens on 5433 there. Port `5433` only exists on the Mac, where Compose publishes PostgreSQL (`127.0.0.1:5433:5432`). Containers reach each other by **service name** on the Compose network: `postgres:5432`.

## 6. What do we know vs what are we only assuming?

| Known | Assumed / ruled out |
| --- | --- |
| Exit code 1, crash loop, fails during startup | ~~"The image is broken"~~: it starts and logs |
| Startup pings the DB | ~~"PostgreSQL is down"~~: `postgres:5432` is open and my host tools work |
| The container got a host-side URL (`127.0.0.1:5433`) | That this is the *only* problem (verify after fixing) |
| That address is unreachable from the container | |

## 7. How do I narrow the problem layer by layer?

```mermaid
flowchart TD
    A[Container restarting] --> B{Exit code?}
    B -- "137 / OOMKilled" --> B1[Memory limits]
    B -- "1" --> C{Logs say why?}
    C -- "yes" --> C1[Follow the error]
    C -- "only startup_failed" --> D[What does startup do?]
    D --> E[Loads config, pings DB]
    E --> F{Effective DATABASE_URL, masked}
    F --> G{Reachable from inside the container network?}
    G -- "no" --> H[Wrong address for this network namespace]
```

**Where the wrong value came from.** **Code fact:** `docker-compose.yml` sets `DATABASE_URL: ${CONTAINER_DATABASE_URL}` for the api and worker. `.env.example` defines two URLs on purpose:

| Variable | Used by | Host:port |
| --- | --- | --- |
| `DATABASE_URL` | Tools running on the Mac (Goose, seeder, tests) | `127.0.0.1:5433` |
| `CONTAINER_DATABASE_URL` | The api and worker containers | `postgres:5432` |

**Observed:** in the original incident, the Compose file had been edited to pass `${DATABASE_URL}` (the host URL) into the container.

## 8. Root cause

The API container received the **host-side** database address. Inside the container, `127.0.0.1:5433` points at the container itself, where nothing is listening. The startup ping fails, the process exits with code 1, and Docker restarts it forever. The fixed-code log line hid the connection error by design.

## 9. Fix / verification

Give containers the container address: `DATABASE_URL: ${CONTAINER_DATABASE_URL}` in Compose, with `CONTAINER_DATABASE_URL` pointing at `postgres:5432` in `.env`. Then:

```sh
docker compose --profile app up -d --force-recreate api
docker compose ps api                           # Up … (healthy), not Restarting
curl --fail-with-body http://127.0.0.1:8080/readyz
```

**To practice it safely**, you can recreate the failure for one run without editing any file, then fix it:

```sh
CONTAINER_DATABASE_URL="$(grep '^DATABASE_URL=' .env | cut -d= -f2-)" \
  docker compose --profile app up -d --force-recreate api      # break it
docker compose --profile app up -d --force-recreate api        # fix it (back to .env values)
```

## 10. Key lesson

- **`localhost` depends on where the process runs.** Inside a container, it means the container.
- **Exit code first, then logs, then config.** Each one narrows the next.
- **Some logs are deliberately terse.** When they are, reason from what startup *does* and inspect the effective config, with secrets masked, instead of guessing.
- **Test connectivity from the same network namespace as the failing process.** A test from your laptop proves nothing about the container.

---

# Incident 5: "Ping works, but Prometheus can't scrape the API"

## 1. Symptom

> "Prometheus on the EC2 box shows the Mac target as DOWN: connection refused. But I can ping the Mac from EC2 over Tailscale."

## 2. What should I check first?

**Separate "can I reach the machine?" from "is anything listening on that port?".** `ping` uses ICMP. It proves there's a route to the host and says nothing about TCP port 8080. So I test the exact thing Prometheus does, from where Prometheus runs:

```sh
# from the EC2 host
curl -v --connect-timeout 5 "http://YOUR_MAC_TAILSCALE_IP:8080/metrics"
```

**Observed:** `Connection refused`. A *refusal* is useful information: the packet reached the Mac, and the Mac actively said "nothing here". A timeout would have pointed at a firewall or routing problem instead.

## 3. Where should I look next?

**On the Mac: what is actually listening on 8080, and on which address?**

```sh
docker compose ps api
lsof -nP -iTCP:8080 -sTCP:LISTEN
```

**Observed:**

```text
published: 127.0.0.1:8080->8080/tcp
listener:  com.docker… 127.0.0.1:8080
```

## 4. What commands, metrics, and logs should I use?

I can reproduce the whole thing on the Mac, without EC2, using any non-loopback address the Mac has (its LAN IP, for example):

```sh
LAN_IP=$(ipconfig getifaddr en0)                  # macOS; Linux: hostname -I
curl -sS -o /dev/null -w 'localhost: %{http_code}\n' http://127.0.0.1:8080/readyz
ping -c 1 "$LAN_IP" >/dev/null && echo "ping LAN_IP: ok"
nc -z -w 2 "$LAN_IP" 8080 && echo "LAN_IP:8080 open" || echo "LAN_IP:8080 refused"
```

## 5. What does each result mean?

**Observed:** `localhost: 200`, `ping LAN_IP: ok`, `LAN_IP:8080 refused`. That's exactly the EC2 symptom, reproduced locally.

```text
Mac interfaces        what's bound to :8080
  lo0   127.0.0.1  ←  Docker listener      ✅ curl localhost works
  en0   LAN IP     ←  (nothing)            ❌ refused
  utun  Tailscale  ←  (nothing)            ❌ refused — what Prometheus hits
```

**Code fact:** Compose publishes the port as `${API_BIND_ADDR:-127.0.0.1}:8080:8080`. A published port listens **only on the address you give it**. The default is loopback, which is safe, but it's unreachable from anywhere else.

## 6. What do we know vs what are we only assuming?

| Known | Assumed / ruled out |
| --- | --- |
| There's a route to the Mac (ping) | ~~"Tailscale is broken"~~: ping works |
| TCP 8080 is refused on non-loopback addresses | ~~"The API is down"~~: localhost returns 200 |
| The only listener is `127.0.0.1:8080` | ~~"A firewall is blocking it"~~: a firewall usually causes timeouts, not instant refusals |

## 7. How do I narrow the problem layer by layer?

```mermaid
flowchart TD
    A[Target DOWN] --> B{Route to host? ping}
    B -- "no" --> B1[Tailscale / routing]
    B -- "yes" --> C{TCP to the port?}
    C -- "timeout" --> C1[Firewall / ACL]
    C -- "refused" --> D{What listens, on which address?}
    D -- "127.0.0.1 only" --> E[Bind address]
    D -- "0.0.0.0 or Tailscale IP" --> F[Check the scraper's network: pod vs host]
```

## 8. Root cause

The API port was published on **loopback only** (`127.0.0.1:8080`). The Mac was reachable over Tailscale, but nothing accepted connections on the Tailscale address, so Prometheus got "connection refused".

## 9. Fix / verification

Set `API_BIND_ADDR` in your git-ignored `.env` to your Mac's Tailscale IPv4 address, then recreate the container and test the **exact** URL the scraper uses:

```sh
docker compose --profile app up -d --force-recreate api
docker compose ps api                                        # should show YOUR_MAC_TAILSCALE_IP:8080
curl -v --connect-timeout 5 "http://YOUR_MAC_TAILSCALE_IP:8080/metrics"   # from EC2
```

In Prometheus, `up{job="mac-go-api"}` should go to `1`. Only the API is published on Tailscale; PostgreSQL stays on `127.0.0.1:5433`.

**Two side effects, both observed later in this lab:**

1. After the change, `curl http://127.0.0.1:8080` on the Mac was **refused**. One address in, one address out. Use the Tailscale URL locally too, or switch back when you're not monitoring remotely.
2. When Tailscale was stopped, the API **couldn't start at all**. The address no longer existed, so Docker failed with `bind: can't assign requested address` and the container stayed in `Created`. Start Tailscale, or override it for one run: `API_BIND_ADDR=127.0.0.1 docker compose --profile app up -d api`.

## 10. Key lesson

- **Ping proves a route, not a listener.** Test the exact protocol, address, and port the client uses.
- **"Refused" and "timed out" point in different directions:** refused means nothing is listening, and timed out means something is filtering.
- **Bind addresses are exclusive.** Binding to one interface means the others can't reach the port, and if that interface disappears, the service can't start.
- **Publish only what needs to be reachable.** The scraper needs `/metrics`, not the database.
