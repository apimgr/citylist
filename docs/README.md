# CityList Documentation

Complete documentation for the CityList API server - a global cities database API with search and filtering capabilities.

## Documentation Pages

- **[Home](index.md)** - Overview, quick start, and examples
- **[API Reference](API.md)** - Complete API endpoint documentation
- **[Server Guide](SERVER.md)** - Server administration and configuration

## Quick Links

- [GitHub Repository](https://github.com/apimgr/citylist)
- [Latest Release](https://github.com/apimgr/citylist/releases/latest)
- [Docker Image](https://ghcr.io/apimgr/citylist)
- [Issue Tracker](https://github.com/apimgr/citylist/issues)

## Building Documentation Locally

```bash
# Install dependencies
cd docs
pip install -r requirements.txt

# Serve locally
mkdocs serve

# Open browser to http://localhost:8000
```

## Documentation Structure

```
docs/
├── index.md              # Documentation home
├── API.md                # API reference
├── SERVER.md             # Server administration
├── mkdocs.yml            # MkDocs configuration
├── requirements.txt      # Python dependencies
├── stylesheets/
│   └── dracula.css       # Dracula theme CSS
└── javascripts/
    └── extra.js          # Custom JavaScript
```

## License

MIT License - See [LICENSE](https://github.com/apimgr/citylist/blob/main/LICENSE.md)
