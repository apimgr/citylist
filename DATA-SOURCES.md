# Data sources

The application code is licensed under MIT. Geographic datasets have separate attribution and license terms and are embedded in the server binary.

## Global cities

`src/data/city.list.json` contains 209,579 public city records with GeoNames identifiers, names, ISO country codes, and coordinates. The source publisher is GeoNames; its daily worldwide database export is available from the [GeoNames data export](https://www.geonames.org/export/). The source snapshot version is identified by SHA-256 `fdc1c3f3f36bb3a85708665e1353bb46efce99ed5c52e31a199a9961b998603d`. The file was present in the repository's initial commit on 2026-09-03; its original retrieval date was not recorded. GeoNames data is available under [Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/). The bundled records are embedded and indexed for read-only lookups.

## US ZIP codes

`src/data/US.zip` is the GeoNames United States postal-code export, embedded and parsed at startup. Its source snapshot is dated 2026-10-10 and has SHA-256 `8f1516859deda23ff858d67ad29b27be621b9eb313ec616b4cb785622f700867`. Geographic records with a two-letter state code are included; APO/FPO/DPO military routing entries do not represent geographic ZIP locations and are skipped. GeoNames identifies this postal export as CC BY 4.0; see the archive's `readme.txt`, [GeoNames postal-code downloads](https://www.geonames.org/export/zip/), and [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

The datasets are reference data and are provided as-is; verify accuracy and coverage for applications that require authoritative postal or administrative boundaries.
