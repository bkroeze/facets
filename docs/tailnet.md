# Tailnet-only deployment

This deployment keeps Facets on loopback and uses [Tailscale Serve](https://tailscale.com/kb/1312/serve) for the only remote entry point. It is intended for one workstation and enrolled phones or other devices on the same Tailnet.

The deployment does **not** use Tailscale Funnel, a LAN listener, port forwarding, or a second Facets account system. Tailnet enrollment and the Tailnet ACL policy are the v1 access boundary.

## Assumptions

- The workstation and phone are enrolled in the same Tailnet.
- MagicDNS and HTTPS certificates for this machine are enabled in the Tailnet.
- The Tailscale CLI is `/usr/bin/tailscale`. If the host installs it elsewhere, edit `ExecStartPre` in `deployment/facets.service` before installing the unit.
- The checkout is at `$HOME/Projects/facets`. Edit `WorkingDirectory` in the unit if it is elsewhere.
- Facets and `kata` are available in `$HOME/.local/bin` or `$HOME/go/bin`; the unit supplies both directories in `PATH`.
- Facets is installed at `$HOME/.local/bin/facets`; the provided Justfile recipe installs it there.
- The workstation's Tailscale daemon starts at boot. The user service is enabled with lingering so it also starts without a login shell.

The unit deliberately sets `127.0.0.1:8080` in both `FACETS_ADDR` and `--addr`. Do not change either value to `:8080`, `0.0.0.0:8080`, or a LAN address.

## Install and enable

From the checkout:

```sh
command -v tailscale
sudo systemctl enable --now tailscaled.service
loginctl enable-linger "$USER"
just tailnet-install
```

`tailnet-install` installs the current checkout as `$HOME/.local/bin/facets`, copies the user unit to `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/facets.service`, reloads the user manager, and enables and starts the unit. It is safe to run again after updating the checkout.

The service's `ExecStartPre` applies this Serve mapping before every Facets start or restart:

```sh
tailscale serve --https=443 http://127.0.0.1:8080
```

The mapping is stored by Tailscale and is not a public HTTPS listener. The unit reapplies it after a workstation restart, while `Restart=on-failure` retries if `tailscaled` is not ready when the user manager starts.

## Local use

The existing foreground command remains supported:

```sh
facets serve --addr 127.0.0.1:8081
```

While the user service is enabled, `127.0.0.1:8080` belongs to that service.
Use another loopback port for a second local instance, or stop the service
temporarily with `systemctl --user stop facets.service`.

## Android base URL

After the service starts, display the MagicDNS endpoint and mapping:

```sh
tailscale serve status
```

Use the HTTPS origin shown there as the Android server URL, for example:

```text
https://<machine-name>.<tailnet-name>.ts.net
```

Enter the origin only: no `/api/v1`, `/healthz`, query, fragment, credentials, or trailing path. Android canonicalizes an optional trailing slash and calls `/healthz` and `/api/v1` itself. The hostname is stable across service and workstation restarts as long as the Tailscale machine name and Tailnet remain unchanged.

## Startup, restart, and health checks

Inspect both layers after boot or an update:

```sh
systemctl --user is-active facets.service
systemctl --user status --no-pager facets.service
tailscale serve status
```

The origin must be loopback-only. On the workstation, this should show `127.0.0.1:8080`, never `0.0.0.0:8080` or `[::]:8080`:

```sh
ss -ltn '( sport = :8080 )'
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
```

From an enrolled Tailnet device, replace `FACETS_HOST` with the hostname from `tailscale serve status`:

```sh
FACETS_HOST='<machine-name>.<tailnet-name>.ts.net'
curl --fail --silent --show-error "https://${FACETS_HOST}/healthz"
curl --fail --silent --show-error "https://${FACETS_HOST}/api/v1"
```

The first response is the Facets health document (`{"status":"ok"}`); the second is the versioned API root. A device that is not on the Tailnet must not resolve or connect to the MagicDNS hostname. From a non-Tailnet network, verify that the HTTPS health request cannot succeed:

```sh
FACETS_HOST='<machine-name>.<tailnet-name>.ts.net'
if curl --connect-timeout 3 --silent --show-error "https://${FACETS_HOST}/healthz"; then
  printf '%s\n' 'unexpected non-Tailnet access'
  exit 1
fi
```

A normal application restart is:

```sh
systemctl --user restart facets.service
```

This reapplies Serve and then starts Facets on loopback. Tailscale Serve can remain configured while Facets is stopped, but it has no reachable origin during that interval.

Facets request logs are JSON records on stderr containing request ID, method, URL path (not the query), status, and duration. They do not log request bodies, task bodies, or completion evidence. Do not set `FACETS_DEBUG=true` in the service environment; it is unnecessary for this deployment and may add provider diagnostics to stderr.

The server does not make authorization or routing decisions from
`X-Forwarded-For`, `X-Forwarded-Host`, or similar client-controlled proxy
headers; Tailscale Serve is only the transport proxy.

## Rollback

To stop the deployed revision while preserving the checkout and user data:

```sh
systemctl --user disable --now facets.service
tailscale serve reset
```

`tailscale serve reset` removes all Serve mappings on this machine. Run it only when this Facets unit owns the machine's Serve configuration. Confirm the origin is gone:

```sh
! curl --connect-timeout 2 --silent http://127.0.0.1:8080/healthz
ss -ltn '( sport = :8080 )'
```

To roll back the binary, install the known-good checkout or release into the same location and start the unit again:

```sh
GOBIN="$HOME/.local/bin" go install ./cmd/facets
systemctl --user enable --now facets.service
```

The start reapplies the same Tailnet mapping. The Facets SQLite registry at `${FACETS_DB:-$HOME/.local/share/facets/facets.db}` is left in place; take a backup before any data migration rollback.

## Uninstall

The reproducible uninstall recipe disables the service, removes its Serve mapping, removes the installed user unit and binary, and reloads the user manager:

```sh
just tailnet-uninstall
```

The equivalent explicit commands are:

```sh
systemctl --user disable --now facets.service
tailscale serve reset
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/facets.service"
systemctl --user daemon-reload
rm -f "$HOME/.local/bin/facets"
```

The uninstall leaves the Facets registry and checkout untouched. Disable lingering separately only when this user has no other user services:

```sh
loginctl disable-linger "$USER"
```

## Threat model and non-goals

- **Protected:** The Facets origin is reachable only from the workstation itself. Tailscale Serve terminates HTTPS and forwards only through the Tailnet, so ordinary LAN peers and public Internet clients have no route to `127.0.0.1:8080`.
- **Access boundary:** Any device enrolled in the Tailnet and permitted by its ACLs can reach this service. Keep Tailnet enrollment and ACL administration restricted to trusted users.
- **Not provided:** Facets does not add user accounts, API tokens, public DNS, public ingress, LAN exposure, application-level authorization, or body redaction beyond its existing safe request logging. Do not use `tailscale funnel` for this deployment.
