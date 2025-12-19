# Docker Safe Sock

`docker-safe-sock` is a security-focused proxy for the Docker socket, written in Go. It allows you to expose the Docker socket to third-party tools (like Traefik) for service discovery without giving them full root-equivalent control over your host.

## Why?
Mounting `/var/run/docker.sock` into a container is a security risk. If that container is compromised, the attacker has full control over the Docker daemon and can effectively take over the host system.

`docker-safe-sock` mitigates this by:
1.  **Read-Only Access**: Only `GET` requests are allowed.
2.  **Allowlist**: Only specific endpoints required for service discovery are exposed.
3.  **Data Minimization**: Automatically strips sensitive Environment Variables (`Env`) from container inspection responses, preventing leaked secrets.
4.  **Event Filtering**: Only proxies container lifecycle events, dropping all other events.

## Features

- **Protocol**: HTTP over TCP (default port `2375`).
- **Allowed Endpoints**:
    - `/version`
    - `/_ping`
    - `/events` (Filtered to container lifecycle only)
    - `/containers/json`
    - `/containers/{id}/json` (Env vars stripped)
- **Blocked**: Everything else (e.g., `exec`, `create`, `kill`, `volumes`, `secrets`, etc.).

## Installation

### 1. Build from Source

Requirements: Go 1.25+

```bash
git clone git@github.com:FransvanderMeer/docker-safe-sock.git
cd docker-safe-sock
go build -o docker-safe-sock .
```

### 2. Install as a System Service

We provide an installation script to set this up as a systemd service that starts automatically with Docker.

```bash
sudo ./install.sh
```

This will:
1.  Install the binary to `/usr/local/bin/docker-safe-sock`.
2.  Install a systemd service file to `/etc/systemd/system/docker-safe-sock.service`.
3.  Start the service.

## Configuration

The service is configured via `/etc/default/docker-safe-sock`.

### Environment Variables & Flags

| Flag | Env Variable | Description | Default |
|------|--------------|-------------|---------|
| `-addr` | `DSS_ADDR` | TCP Address(es) to listen on (comma separated). | `127.0.0.1:2375` (if no socket set) |
| `-socket` | `DSS_SOCKET` | Path to the REAL Docker socket to proxy to. | `/var/run/docker.sock` |
| `-safe-socket` | `DSS_SAFE_SOCKET` | Path to create the safe Unix socket listener. | *(empty)* |

### Listening on Docker Bridges (`auto:bridge`)

If you want the proxy to be accessible from inside Docker containers via TCP without exposing it to the entire LAN, you can use the `auto:bridge` keyword.

```bash
# Listen on localhost AND all Docker bridge interfaces
DSS_ADDR=127.0.0.1:2375,auto:bridge
```

This will automatically find all network interfaces starting with `docker` or `br-` (e.g., `docker0`, `br-custom`) and bind the proxy to their IP addresses.

## Usage with Traefik

Once running, point Traefik to the proxy instead of mounting the socket.

**Docker Compose Example (TCP):**

```yaml
services:
  traefik:
    image: traefik:v3.0
    command:
      # Use TCP endpoint instead of unix socket
      - "--providers.docker.endpoint=tcp://host.docker.internal:2375"
      - "--api.insecure=true"
    ports:
      - "80:80"
      - "8080:8080"
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

**Docker Compose Example (Safe Socket):**

If you mapped the safe socket to `/var/run/docker-safe.sock`:

```yaml
services:
  traefik:
    image: traefik:v3.0
    volumes:
      - /var/run/docker-safe.sock:/var/run/docker.sock # Mount the SAFE socket
    command:
      - "--providers.docker.endpoint=unix:///var/run/docker.sock"
```

## Manual Usage

Run the binary directly:
```bash
# Listen on TCP
./docker-safe-sock -addr :2375

# Listen on Unix Socket
./docker-safe-sock -safe-socket /var/run/docker-safe.sock
```

## Security Note

While this tool significantly reduces the attack surface, exposing the Docker API (even read-only) still reveals information about your running infrastructure. Ensure the `docker-safe-sock` port (default 2375) is **NOT** exposed to the public internet. It should only be accessible by trusted containers or localhost.
