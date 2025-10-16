# CityList Server Administration Guide

Complete guide for installing, configuring, and managing the CityList API server.

## Installation

### Docker Installation (Recommended)

#### Production Deployment

```bash
# Clone repository
git clone https://github.com/apimgr/citylist.git
cd citylist

# Start with Docker Compose
docker-compose up -d

# Check logs
docker-compose logs -f citylist

# View credentials
cat ./rootfs/config/citylist/admin_credentials
```

#### Development/Testing

```bash
# Build development image
make docker-dev

# Start test environment
docker-compose -f docker-compose.test.yml up -d

# Access at http://localhost:64181
```

### Binary Installation

#### Linux/BSD

```bash
# Download latest release
wget https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64

# Make executable
chmod +x citylist-linux-amd64

# Move to system path
sudo mv citylist-linux-amd64 /usr/local/bin/citylist

# Create directories (as root)
sudo mkdir -p /etc/citylist /var/lib/citylist /var/log/citylist

# Run
sudo citylist --port 80

# Or run as user (random port)
citylist
```

#### macOS

```bash
# Download for Apple Silicon
wget https://github.com/apimgr/citylist/releases/latest/download/citylist-darwin-arm64

# Or for Intel Mac
wget https://github.com/apimgr/citylist/releases/latest/download/citylist-darwin-amd64

chmod +x citylist-darwin-*
./citylist-darwin-* --port 8080
```

#### Windows

```powershell
# Download from GitHub releases
# https://github.com/apimgr/citylist/releases/latest

# Run
.\citylist-windows-amd64.exe --port 8080
```

## Configuration

### Environment Variables

```bash
# Directory paths
CONFIG_DIR=/etc/citylist          # Configuration directory
DATA_DIR=/var/lib/citylist        # Data directory
LOGS_DIR=/var/log/citylist        # Logs directory

# Server settings
PORT=8080                         # HTTP port
ADDRESS=0.0.0.0                   # Listen address

# Database
DB_PATH=/var/lib/citylist/citylist.db  # SQLite database path

# Admin credentials (first run only)
ADMIN_USER=administrator          # Admin username
ADMIN_PASSWORD=changeme           # Admin password
ADMIN_TOKEN=your-token-here       # API token
```

### Command-Line Flags

```bash
citylist [options]

Options:
  --port PORT           HTTP port (default: random 64000-64999)
  --address ADDR        Listen address (default: 0.0.0.0)
  --config DIR          Configuration directory
  --data DIR            Data directory
  --logs DIR            Logs directory
  --db-path PATH        SQLite database path
  --status              Check server status (for health checks)
  --version             Show version information
  --help                Show help message
```

### Directory Structure

#### Linux/BSD (with root privileges)
```
Config:  /etc/citylist/
Data:    /var/lib/citylist/
Logs:    /var/log/citylist/
Runtime: /run/citylist/
```

#### Linux/BSD (without root)
```
Config:  ~/.config/citylist/
Data:    ~/.local/share/citylist/
Logs:    ~/.local/state/citylist/
Runtime: ~/.local/run/citylist/
```

#### macOS
```
Config:  ~/Library/Application Support/Citylist/
Data:    ~/Library/Application Support/Citylist/data/
Logs:    ~/Library/Logs/Citylist/
Runtime: ~/Library/Application Support/Citylist/run/
```

#### Windows
```
Config:  %APPDATA%\Citylist\config\
Data:    %APPDATA%\Citylist\data\
Logs:    %APPDATA%\Citylist\logs\
```

#### Docker
```
Config:  /config
Data:    /data
Logs:    /logs
```

## Admin Authentication

### First Run Setup

On first startup, admin credentials are automatically generated:

1. Username: `administrator` (or `$ADMIN_USER`)
2. Password: Random 16-character string (or `$ADMIN_PASSWORD`)
3. Token: Random 64-character hex string (or `$ADMIN_TOKEN`)

Credentials are saved to:
- Database (hashed with SHA-256)
- Config file: `{CONFIG_DIR}/admin_credentials` (permissions: 0600)
- Console output (shown once - save securely!)

### Accessing Admin Panel

#### Web UI (Basic Auth)
```
URL: http://your-server:port/admin
Username: administrator
Password: <from credentials file>
```

Browser will prompt for credentials automatically.

#### API (Bearer Token)
```bash
# Get admin info
curl -H "Authorization: Bearer <token>" \
  http://your-server:port/api/v1/admin

# Update settings
curl -X PUT \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"settings":{"server.title":"My API"}}' \
  http://your-server:port/api/v1/admin/settings
```

### Resetting Credentials

To reset admin credentials:

```bash
# Stop server
docker-compose down  # (if using Docker)

# Delete database
rm ./rootfs/data/citylist/db/citylist.db

# Restart server (new credentials will be generated)
docker-compose up -d
```

## Server Settings

Settings can be modified through:

1. **Web UI**: Navigate to `/admin/settings`
2. **API**: `PUT /api/v1/admin/settings`
3. **Environment variables** (first run only)

### Available Settings

```yaml
Server:
  server.title: "CityList API"
  server.address: "0.0.0.0"
  server.http_port: 64180

Database:
  db.type: "sqlite"
  db.path: "/data/db/citylist.db"

Logging:
  log.level: "info"
  log.format: "json"
  log.access: true

Search:
  search.default_limit: 50
  search.max_limit: 1000
```

## Logging

### Log Files

- `access.log` - HTTP access logs
- `error.log` - Application errors
- `audit.log` - Admin actions

### View Logs

```bash
# Docker
docker-compose logs -f citylist

# Or directly
tail -f ./rootfs/logs/citylist/access.log

# Binary installation
tail -f /var/log/citylist/access.log
```

## Backup & Restore

### Backup Database

```bash
# Docker
docker-compose exec citylist cp /data/db/citylist.db /data/backup.db

# Binary
cp /var/lib/citylist/citylist.db /var/lib/citylist/backup.db
```

### Restore Database

```bash
# Stop server
docker-compose down

# Restore backup
cp backup.db ./rootfs/data/citylist/db/citylist.db

# Restart
docker-compose up -d
```

## Monitoring

### Health Check

```bash
# Simple check
curl http://localhost:8080/healthz

# Using binary flag
citylist --status
```

### Statistics

```bash
# Get database stats
curl http://localhost:8080/api/v1/stats
```

## Security

### Best Practices

1. **Change default admin password immediately**
2. **Rotate API tokens periodically**
3. **Use HTTPS in production** (reverse proxy)
4. **Restrict admin routes to internal network**
5. **Set proper file permissions** (0600 for credentials)
6. **Bind to 127.0.0.1 for local-only access**
7. **Configure firewall rules**

### File Permissions

```bash
# Credentials file
chmod 600 /etc/citylist/admin_credentials

# Database file
chmod 644 /var/lib/citylist/citylist.db

# Log files
chmod 644 /var/log/citylist/*.log
```

## Troubleshooting

### Port Already in Use

```bash
# Check what's using the port
lsof -i :8080

# Use different port
citylist --port 8081
```

### Permission Denied

```bash
# Run as root for privileged ports
sudo citylist --port 80

# Or use non-privileged port
citylist --port 8080
```

### Database Locked

```bash
# Ensure only one instance is running
pkill citylist

# Remove lock file (if exists)
rm /var/lib/citylist/citylist.db-journal
```

## Upgrading

### Docker

```bash
# Pull latest image
docker-compose pull citylist

# Restart container
docker-compose up -d
```

### Binary

```bash
# Download new version
wget https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64

# Replace binary
sudo mv citylist-linux-amd64 /usr/local/bin/citylist

# Restart service
sudo systemctl restart citylist
```

## Systemd Service (Linux)

Create `/etc/systemd/system/citylist.service`:

```ini
[Unit]
Description=CityList API Server
After=network.target

[Service]
Type=simple
User=citylist
Group=citylist
ExecStart=/usr/local/bin/citylist --port 8080
Restart=always
RestartSec=10

Environment="CONFIG_DIR=/etc/citylist"
Environment="DATA_DIR=/var/lib/citylist"
Environment="LOGS_DIR=/var/log/citylist"

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable citylist
sudo systemctl start citylist
sudo systemctl status citylist
```

## Support

- GitHub Issues: [https://github.com/apimgr/citylist/issues](https://github.com/apimgr/citylist/issues)
- Documentation: [https://citylist.readthedocs.io](https://citylist.readthedocs.io)
