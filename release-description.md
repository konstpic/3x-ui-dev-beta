## Release v1.0.0

**SharX** is a fork of the original **3XUI** panel with enhanced features and monitoring capabilities.

### ✨ Key Features

- **Node Mode**: One panel manages multiple nodes
- **PostgreSQL**: Full migration from SQLite
- **Redis Integration**: Enhanced performance with caching
- **Grafana Integration**: Advanced monitoring with Prometheus metrics and Loki logs
- **Docker-Based**: Easy deployment with pre-built images
- **HWID Protection**: Device identification (Beta, Happ & V2RayTun)
- **Auto SSL**: Let's Encrypt certificates with auto-renewal
- **Environment-Based Configuration**: Flexible domain, port, and certificate management via environment variables

### 🐳 Docker Images

Images are available in Harbor:

- **sharx**: `registry.konstpic.ru/sharx/sharx:1.0.0` / `registry.konstpic.ru/sharx/sharx:latest`
- **sharxnode**: `registry.konstpic.ru/sharx/sharxnode:1.0.0` / `registry.konstpic.ru/sharx/sharxnode:latest`
- **postgres**: `registry.konstpic.ru/sharx/postgres:16-alpine` / `registry.konstpic.ru/sharx/postgres:latest`

### 📦 Quick Start

Update your `docker-compose.yml`:

```yaml
services:
  sharx:
    image: registry.konstpic.ru/sharx/sharx:latest
  sharxnode:
    image: registry.konstpic.ru/sharx/sharxnode:latest
  postgres:
    image: registry.konstpic.ru/sharx/postgres:latest
```

### 📝 Installation

For detailed installation instructions, see the [README](https://github.com/konstpic/SharX#quick-start--быстрый-старт).

Quick install:

```bash
git clone https://github.com/konstpic/SharX.git
cd SharX
sudo bash ./install_ru.sh
```

### 🔄 Changes

See commit history for detailed changes.
