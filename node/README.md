# 3x-ui Node Service

Node service (worker) for 3x-ui multi-node architecture.

## Description

This service runs on separate servers and manages XRAY Core instances. The 3x-ui panel (master) sends configurations to nodes via REST API.

## Features

- REST API for XRAY Core management
- Apply configurations from the panel
- Reload XRAY without stopping the container
- Status and health checks

## API Endpoints

### `GET /health`
Health check endpoint (no authentication required)

### `POST /api/v1/apply`
Apply new XRAY configuration
- **Headers**: `Authorization: Bearer <api-key>`
- **Body**: XRAY JSON configuration

### `POST /api/v1/reload`
Reload XRAY
- **Headers**: `Authorization: Bearer <api-key>`

### `POST /api/v1/force-reload`
Force reload XRAY (stops and restarts)
- **Headers**: `Authorization: Bearer <api-key>`

### `GET /api/v1/status`
Get XRAY status
- **Headers**: `Authorization: Bearer <api-key>`

### `GET /api/v1/stats`
Get traffic statistics and online clients
- **Headers**: `Authorization: Bearer <api-key>`
- **Query Parameters**: `reset=true` to reset statistics after reading

## Running

### Docker Compose

```bash
cd node
NODE_API_KEY=your-secure-api-key docker-compose up -d --build
```

#### HTTPS Configuration

To enable HTTPS for the node API:

1. Create a `cert` directory in the `node` folder:
   ```bash
   mkdir -p node/cert
   ```

2. Place your TLS certificate files in `node/cert/`:
   - Certificate file: `cert.pem` (or `fullchain.pem`)
   - Private key file: `key.pem` (or `privkey.pem`)

3. Uncomment and configure environment variables in `docker-compose.yml`:
   ```yaml
   environment:
     - NODE_TLS_CERT_FILE=/app/cert/cert.pem
     - NODE_TLS_KEY_FILE=/app/cert/key.pem
   ```

4. Uncomment the HTTPS port mapping:
   ```yaml
   ports:
     - "8080:8080"  # HTTP (optional, can be removed if using HTTPS only)
     - "8443:8443"  # HTTPS
   ```

5. Restart the container:
   ```bash
   docker-compose down
   docker-compose up -d
   ```

**Note:** XRAY Core is automatically downloaded during Docker image build for your architecture. Docker BuildKit automatically detects the host architecture. To explicitly specify the architecture, use:

```bash
DOCKER_BUILDKIT=1 docker build --build-arg TARGETARCH=arm64 -t 3x-ui-node -f node/Dockerfile ..
```

### Manual

```bash
go run node/main.go -port 8080 -api-key your-secure-api-key
```

## Environment Variables

- `NODE_API_KEY` - API key for authentication (required)
- `NODE_TLS_CERT_FILE` - Path to TLS certificate file (optional, enables HTTPS)
- `NODE_TLS_KEY_FILE` - Path to TLS private key file (optional, enables HTTPS)

## Structure

```
node/
├── main.go           # Entry point
├── api/
│   └── server.go     # REST API server
├── xray/
│   └── manager.go    # XRAY process management
├── Dockerfile        # Docker image
└── docker-compose.yml
```
