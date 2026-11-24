---
name: citylist
description: Project-specific data and route notes discovered while maintaining Citylist
type: project
---

# Citylist Project Memory

- The server embeds the GeoNames city snapshot and the US ZIP snapshot at build time; normal requests do not download data.
- GeoNames IDs are preserved because consumers use them as stable lookup identifiers.
- The canonical native REST prefix is `/api/v1`; the browser interface is server-rendered from Go templates.
- The required client binary is `citylist-cli`, built separately from the `citylist` server.
