# CityList API Reference

Complete API documentation for CityList - Global cities database API.

## Base URL

```
http://your-server:port/api/v1
```

## Authentication

All city data endpoints are **public** and require no authentication.

Admin endpoints require authentication:
- **Bearer Token**: `Authorization: Bearer <token>`
- **Basic Auth**: `Authorization: Basic <base64(user:pass)>`

## Endpoints

### Public Routes

#### GET /api/v1

Get API information and version.

**Response:**
```json
{
  "success": true,
  "data": {
    "name": "CityList API",
    "version": "1.0.0",
    "endpoints": [...]
  }
}
```

#### GET /api/v1/search

Search cities by name, country, or other criteria.

**Query Parameters:**
- `q` (string) - Search term
- `country` (string) - Filter by country code (ISO 3166-1 alpha-2)
- `state` (string) - Filter by state/province
- `min_population` (integer) - Minimum population
- `max_population` (integer) - Maximum population
- `limit` (integer) - Results limit (default: 50, max: 1000)
- `offset` (integer) - Pagination offset (default: 0)

**Example:**
```bash
curl "http://localhost:8080/api/v1/search?q=london&limit=10"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "cities": [...],
    "total": 42,
    "limit": 10,
    "offset": 0
  }
}
```

#### GET /api/v1/city/:id

Get details for a specific city.

**Parameters:**
- `id` (path) - City ID

**Example:**
```bash
curl "http://localhost:8080/api/v1/city/12345"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 12345,
    "name": "London",
    "country": "GB",
    "population": 8982000,
    "latitude": 51.5074,
    "longitude": -0.1278
  }
}
```

#### GET /api/v1/stats

Get database statistics.

**Example:**
```bash
curl "http://localhost:8080/api/v1/stats"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "total_cities": 200000,
    "countries": 249,
    "last_updated": "2024-01-01T00:00:00Z"
  }
}
```

### Export Endpoints

#### GET /api/v1/cities.json

Export all cities as JSON.

#### GET /api/v1/cities.csv

Export all cities as CSV.

#### GET /api/v1/cities.geojson

Export all cities as GeoJSON.

### Admin Routes (Authentication Required)

#### GET /api/v1/admin

Get admin dashboard information.

**Headers:**
```
Authorization: Bearer <token>
```

#### GET /api/v1/admin/settings

Get all server settings.

#### PUT /api/v1/admin/settings

Update server settings.

**Request Body:**
```json
{
  "settings": {
    "server.title": "My CityList API",
    "search.default_limit": 100
  }
}
```

## Error Responses

All errors follow this format:

```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "Human readable error message",
    "field": "field_name"
  },
  "timestamp": "2024-01-01T12:00:00Z"
}
```

### Common Error Codes

- `INVALID_INPUT` - Invalid input parameters
- `NOT_FOUND` - Resource not found
- `UNAUTHORIZED` - Authentication required
- `FORBIDDEN` - Insufficient permissions
- `INTERNAL_ERROR` - Server error

## Rate Limiting

No rate limiting is currently enforced on public endpoints.

## Pagination

Use `limit` and `offset` parameters:

```bash
# First page (50 results)
curl "http://localhost:8080/api/v1/search?q=city&limit=50&offset=0"

# Second page (next 50 results)
curl "http://localhost:8080/api/v1/search?q=city&limit=50&offset=50"
```

## Response Format

All successful responses include:

```json
{
  "success": true,
  "data": { ... },
  "timestamp": "2024-01-01T12:00:00Z"
}
```

## CORS

CORS is enabled for all origins in development mode.

## Health Check

```bash
curl "http://localhost:8080/healthz"
```

Returns `200 OK` when server is healthy.
