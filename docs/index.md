# CityList API Documentation

Welcome to the CityList API documentation. This API provides access to a global database of 200,000+ cities with comprehensive search and filtering capabilities.

## Overview

CityList is a high-performance REST API server that provides:

- **200,000+ cities** from around the world
- **Fast search** with in-memory indexes
- **Geographic queries** by coordinates, country, state, province
- **Population filters** and sorting
- **Multiple export formats** (JSON, CSV, GeoJSON)
- **Admin dashboard** for configuration
- **Single binary deployment** with embedded data

## Quick Start

### Docker Deployment

```bash
# Pull and run
docker run -d \
  --name citylist \
  -p 64180:80 \
  -v ./config:/config \
  -v ./data:/data \
  ghcr.io/apimgr/citylist:latest
```

### Binary Installation

```bash
# Download latest release
wget https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64

# Make executable
chmod +x citylist-linux-amd64

# Run
./citylist-linux-amd64 --port 8080
```

## API Examples

### Search Cities

```bash
# Search by name
curl "http://localhost:8080/api/v1/search?q=london"

# Filter by country
curl "http://localhost:8080/api/v1/search?country=US&limit=100"

# Search with population filter
curl "http://localhost:8080/api/v1/search?q=city&min_population=1000000"
```

### Get City Details

```bash
# Get specific city by ID
curl "http://localhost:8080/api/v1/city/12345"
```

### Export Data

```bash
# Export as CSV
curl "http://localhost:8080/api/v1/cities.csv" > cities.csv

# Export as GeoJSON
curl "http://localhost:8080/api/v1/cities.geojson" > cities.geojson
```

## Features

- **Public API**: All city data is freely accessible (no authentication required)
- **Admin Interface**: Server configuration protected by token/password authentication
- **Embedded Data**: 200k+ cities built into the binary
- **Fast Search**: In-memory indexes for instant lookups
- **Geographic Queries**: Search by coordinates, country, region
- **Web Frontend**: Go html/template based UI with dark theme
- **Export Formats**: JSON, CSV, GeoJSON

## Documentation

- [API Reference](API.md) - Complete API endpoint documentation
- [Server Guide](SERVER.md) - Server administration and configuration
- [GitHub Repository](https://github.com/apimgr/citylist) - Source code and issues

## Support

- GitHub Issues: [https://github.com/apimgr/citylist/issues](https://github.com/apimgr/citylist/issues)
- Documentation: [https://citylist.readthedocs.io](https://citylist.readthedocs.io)

## License

MIT License - See [LICENSE](https://github.com/apimgr/citylist/blob/main/LICENSE.md)
