# Citylist

Citylist is a read-only Go API and server-rendered web app for searching the GeoNames global city dataset and geographic United States ZIP codes. GeoNames city identifiers are preserved for compatibility with OpenWeatherMap city lookups. Both datasets are embedded in the server binary; no external service is called at runtime.

## Features

- Search and look up cities by GeoNames ID, country, or coordinates.
- Look up a US ZIP code, search ZIP codes by city/state, list by state, and find nearby ZIP locations.
- REST API under `/api/v1`, GraphQL at `/graphql`, and OpenAPI documentation at `/api/v1/server/swagger`.
- Browser UI, PWA manifest, and content-negotiated server information pages.
- Companion `citylist-cli` for city and ZIP queries.

## Run

Build with `make build`, then run `./binaries/citylist --help`. The default listen port is 8080 and can be set with `--port` or `PORT`. The CLI accepts `--server URL`; for example:

```sh
./binaries/citylist-cli --server http://127.0.0.1:8080 city 2643743
./binaries/citylist-cli --server http://127.0.0.1:8080 zipcode 10001
```

## API examples

```sh
curl -q -LSsf 'http://127.0.0.1:8080/api/v1/cities/search?q=London'
curl -q -LSsf 'http://127.0.0.1:8080/api/v1/zipcodes/10001'
curl -q -LSsf 'http://127.0.0.1:8080/api/v1/zipcodes/search?q=New%20York&state=NY'
```

The city dataset is available from [GeoNames](https://www.geonames.org/export/) and the US ZIP dataset from [GeoNames postal codes](https://download.geonames.org/export/zip/). Both are licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/); attribution and import scope are documented in [DATA-SOURCES.md](DATA-SOURCES.md).

The application code is licensed under MIT. See [LICENSE.md](LICENSE.md).
