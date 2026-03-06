# URL Shortener

A production-ready URL shortening service (like bit.ly) built in **Go**, featuring a clean layered architecture, SQLite persistence, structured logging, and full test coverage.

## Stack

| Layer | Technology |
|---|---|
| Language | Go 1.24+ |
| HTTP Router | [chi](https://github.com/go-chi/chi) |
| Database | SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO) |
| Tests | `testing` + [testify](https://github.com/stretchr/testify) |
| ID Generation | Base62 (a-z, A-Z, 0-9), 7 characters |
| Logging | `log/slog` (stdlib, structured JSON in production) |
| Container | Docker multi-stage build + docker-compose |

## Running the Project

### Go run (local)

```bash
# Install dependencies
go mod download

# Run the server (default port 8080)
go run ./cmd/api

# Or with custom configuration
PORT=9090 BASE_URL=http://myhost:9090 API_KEY=secret go run ./cmd/api
```

### Docker Compose

```bash
docker-compose up --build
```

The API will be available at `http://localhost:8080`.

## Running Tests

```bash
# Run all tests (unit + integration)
go test ./...

# Run with verbose output
go test ./... -v

# Run a specific package
go test ./internal/service/...
go test ./tests/...
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server port |
| `BASE_URL` | `http://localhost:8080` | Base URL used to build short URLs |
| `API_KEY` | `default-api-key` | API key required in `X-API-Key` header for write operations |
| `DB_PATH` | `./data/urls.db` | Path to the SQLite database file |
| `LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |

## API Reference

### Authentication

POST and list endpoints require the `X-API-Key` header:

```
X-API-Key: default-api-key
```

### Create Short URL

```
POST /v1/urls
```

**Request:**
```json
{
  "originalUrl": "https://www.example.com/my/long/path",
  "expirationDate": "2025-12-31T23:59:59Z",
  "customAlias": "my-alias"
}
```

- `originalUrl` — **required**, must be a valid http/https URL
- `expirationDate` — optional ISO 8601 timestamp
- `customAlias` — optional custom short ID (returns 409 Conflict if already taken)

**Response `201 Created`:**
```json
{
  "id": "aB3xY7z",
  "shortUrl": "http://localhost:8080/aB3xY7z",
  "originalUrl": "https://www.example.com/my/long/path",
  "createdAt": "2024-03-01T10:00:00Z",
  "expirationDate": "2025-12-31T23:59:59Z"
}
```

**curl example:**
```bash
curl -X POST http://localhost:8080/v1/urls \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{"originalUrl":"https://www.example.com/my/long/path"}'
```

---

### Redirect

```
GET /{id}
```

- `301 Moved Permanently` → redirects to `originalUrl`
- `404 Not Found` → short ID does not exist
- `410 Gone` → URL has expired

**curl example:**
```bash
curl -L http://localhost:8080/aB3xY7z
```

---

### Get URL Details

```
GET /v1/urls/{id}
```

**Response `200 OK`:**
```json
{
  "id": "aB3xY7z",
  "shortUrl": "http://localhost:8080/aB3xY7z",
  "originalUrl": "https://www.example.com/my/long/path",
  "createdAt": "2024-03-01T10:00:00Z",
  "expirationDate": "2025-12-31T23:59:59Z",
  "clickCount": 42
}
```

**curl example:**
```bash
curl http://localhost:8080/v1/urls/aB3xY7z \
  -H "X-API-Key: default-api-key"
```

---

### List URLs (paginated)

```
GET /v1/urls?page=1&size=10
```

**Response `200 OK`:**
```json
{
  "data": [...],
  "page": 1,
  "size": 10,
  "total": 50
}
```

**curl example:**
```bash
curl "http://localhost:8080/v1/urls?page=1&size=10" \
  -H "X-API-Key: default-api-key"
```

---

### Error Response Format

All errors follow a consistent envelope:

```json
{
  "error": {
    "code": "URL_NOT_FOUND",
    "message": "The requested short URL does not exist"
  }
}
```

| Status | Code | Scenario |
|---|---|---|
| 400 | `INVALID_URL` | URL is empty, malformed, or not http/https |
| 400 | `INVALID_REQUEST` | Request body is not valid JSON |
| 401 | `UNAUTHORIZED` | Missing or invalid `X-API-Key` |
| 404 | `URL_NOT_FOUND` | Short ID not found |
| 409 | `ALIAS_CONFLICT` | Custom alias is already taken |
| 410 | `URL_EXPIRED` | Short URL has passed its expiration date |
| 500 | `INTERNAL_SERVER_ERROR` | Unexpected server error |

## Architecture Decisions

### Layered Architecture

The code is organized into three clear layers, each with a single responsibility:

```
cmd/api/main.go          → Wires everything together, starts HTTP server
internal/handler/        → HTTP layer: parses requests, calls service, writes responses
internal/service/        → Business logic: URL validation, ID generation, expiration
internal/repository/     → Persistence layer: SQLite CRUD operations
internal/model/          → Shared domain types and DTOs
internal/middleware/      → Cross-cutting concerns: auth, logging, recovery
```

Each layer communicates via **interfaces** (e.g., `URLRepository`, `URLService`), making it easy to swap implementations and write unit tests with mocks or in-memory databases.

### ID Generation

Short IDs are generated using a Base62 alphabet (`a-z A-Z 0-9`), 7 characters long, giving 62⁷ ≈ 3.5 trillion possible combinations. On collision (extremely unlikely), the service retries up to 10 times. Custom aliases bypass random generation entirely.

### Persistence

SQLite is used as an embedded database — no external process or network connection required. The `modernc.org/sqlite` driver is pure Go (no CGO), making cross-compilation and containerization straightforward. The repository layer exposes an interface so the underlying store can be replaced (e.g., with PostgreSQL) without touching the service or handler layers.

### Authentication

A simple static API key (`X-API-Key` header) protects mutating endpoints (POST, list). The redirect endpoint (`GET /{id}`) is intentionally public — it needs to work in browsers and CLI tools without credentials. The API key defaults to `default-api-key` and can be overridden via the `API_KEY` environment variable.

### Structured Logging

`log/slog` (Go 1.21+ stdlib) provides structured logging with contextual fields. In a terminal the logger uses human-readable text format; otherwise (container/CI) it uses JSON for easier ingestion by log aggregators.

## Project Structure

```
url-shortener/
├── cmd/api/main.go              # Entrypoint
├── internal/
│   ├── handler/                 # HTTP handlers
│   │   ├── url_handler.go
│   │   └── url_handler_test.go
│   ├── service/                 # Business logic
│   │   ├── url_service.go
│   │   └── url_service_test.go
│   ├── repository/              # SQLite persistence
│   │   ├── url_repository.go
│   │   └── url_repository_test.go
│   ├── model/                   # Domain structs and DTOs
│   │   └── url.go
│   └── middleware/              # Auth, logging, recovery
│       └── middleware.go
├── tests/
│   └── integration_test.go      # End-to-end API tests via httptest
├── go.mod
├── go.sum
├── Dockerfile                   # Multi-stage build
├── docker-compose.yml
└── README.md
```
