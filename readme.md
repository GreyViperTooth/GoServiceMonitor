# GoServiceMonitor

A lightweight HTTP health monitoring service written in Go — zero external dependencies, production-ready concurrency, and a clean REST API for managing and tracking the uptime of multiple web services.

## Features

- **Automated health checks** — background goroutine polls every registered service every 30 seconds
- **Concurrent checks** — all services checked in parallel via goroutines and a buffered channel
- **Uptime reporting** — per-service uptime percentage and average response time calculated from up to 100 stored results
- **Thread-safe** — `sync.Mutex` guards shared service registry and history
- **Graceful shutdown** — handles `SIGINT`/`SIGTERM` with a 15-second drain window
- **Structured logging** — JSON logs via `log/slog` to stdout
- **Zero dependencies** — standard library only (`net/http`, `encoding/json`, `log/slog`, `sync`, `context`)

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Liveness probe — returns `{"status":"ok","time":"..."}` |
| `GET` | `/services` | List all registered services |
| `POST` | `/services` | Register a new service `{"name":"...","url":"..."}` |
| `GET` | `/services/{name}/check` | Immediately check a single service |
| `GET` | `/check-all` | Concurrently check all services and return results |
| `GET` | `/report` | Uptime statistics for all services |

## Live Demo

The service is deployed on an AWS EC2 instance, running as a systemd service behind nginx on port 80.

**Base URL:** `http://18.188.213.92`

> **Note:** The instance will terminate when I run out of AWS credits — if the URL is unreachable, that's likely why.

Try it:
```bash
curl http://18.188.213.92/health
curl http://18.188.213.92/report
```

## Getting Started

**Prerequisites:** Go 1.21+

### Linux

```bash
# Clone the repo
git clone https://github.com/<your-username>/GoServiceMonitor.git
cd GoServiceMonitor

# Build
go build -o service-monitor .

# Run (default port 8080)
./service-monitor

# Custom port
PORT=9000 ./service-monitor
```

### Windows

```powershell
go build -o service-monitor.exe .
.\service-monitor.exe

# Custom port
$env:PORT=9000; .\service-monitor.exe
```

## Usage Example

```bash
# Register services to monitor
curl -X POST http://localhost:8080/services \
  -H "Content-Type: application/json" \
  -d '{"name":"github","url":"https://github.com"}'

# For Windows cmd:
curl -X POST http://localhost:8080/services -H "Content-Type: application/json" -d "{\"name\":\"google\",\"url\":\"https://www.google.com\"}"

# For Windows PowerShell:
Invoke-RestMethod -Uri "http://localhost:8080/services" -Method Post -ContentType "application/json" -Body '{"name":"google","url":"https://www.google.com"}'

# Run an immediate check on all services
curl http://localhost:8080/check-all

# View uptime report
curl http://localhost:8080/report
```

### Sample report response

```json
[
  {
    "name": "github",
    "url": "https://github.com",
    "uptime_percent": 100.0,
    "avg_response_ms": 142,
    "total_checks": 12,
    "last_checked": "2026-06-07T14:03:00Z"
  }
]
```

## Health Check Logic

- **Healthy:** HTTP 2xx or 3xx response within 5 seconds
- **Unhealthy:** HTTP 4xx/5xx, timeout, or connection error
- **History:** Last 100 results stored per service

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |

## Project Structure

```
GoServiceMonitor/
└── main.go     # Entire application — ~310 lines, stdlib only
```

## License

MIT
