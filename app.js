const express = require('express');
const helmet = require('helmet');
const rateLimit = require('express-rate-limit');
const cors = require('cors');
const path = require('path');
const swaggerUi = require('swagger-ui-express');
const swaggerJsdoc = require('swagger-jsdoc');
const expressLayouts = require('express-ejs-layouts');

const app = express();
const PORT = process.env.PORT || 3000;
const HOST = process.env.HOST || '0.0.0.0';

// Trust proxy for reverse proxy support
app.set('trust proxy', true);

app.use(helmet({
  contentSecurityPolicy: {
    directives: {
      ...helmet.contentSecurityPolicy.getDefaultDirectives(),
      "script-src": ["'self'", "'unsafe-inline'"],
      "script-src-attr": ["'unsafe-inline'"],
    },
  },
}));

const limiter = rateLimit({
  windowMs: 60 * 60 * 1000, // 1 hour
  max: 1000, // limit each IP to 1000 requests per windowMs
  message: 'Too many requests from this IP, please try again after an hour.'
});
app.use(limiter);

app.use(cors());

app.use(express.json());
app.use(express.urlencoded({ extended: true }));

app.use(expressLayouts);
app.set('layout', 'layout');
app.set('view engine', 'ejs');
app.set('views', path.join(__dirname, 'views'));

app.use(express.static(path.join(__dirname, 'public')));

const swaggerOptions = {
  definition: {
    openapi: '3.0.0',
    info: {
      title: 'CityList API',
      version: '1.0.0',
      description: 'API for worldwide city data with geographic coordinates',
    },
    servers: [
      {
        url: '/api/v1',
        description: 'API v1',
      },
    ],
  },
  apis: ['./routes/api/v1/*.js'],
};

const specs = swaggerJsdoc(swaggerOptions);

const swaggerUiOptions = {
  customCss: `
    /* Complete Swagger UI Dracula Theme Override */
    html, body {
      background-color: #282a36 !important;
    }
    
    .swagger-ui {
      background-color: #282a36 !important;
      color: #f8f8f2 !important;
      font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace !important;
    }

    .swagger-ui .wrapper {
      background-color: #282a36 !important;
    }

    .swagger-ui .info {
      background-color: #282a36 !important;
    }

    .swagger-ui .info .title {
      color: #bd93f9 !important;
      font-size: 2.5rem !important;
      text-align: center !important;
    }

    .swagger-ui .info .description {
      color: #f8f8f2 !important;
      text-align: center !important;
    }

    .swagger-ui .info .base-url {
      color: #8be9fd !important;
    }

    .swagger-ui .scheme-container {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      border-radius: 8px !important;
    }

    .swagger-ui .opblock-tag {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      border-radius: 8px !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui .opblock-tag:hover {
      background-color: #6272a4 !important;
    }

    .swagger-ui .opblock {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      border-radius: 8px !important;
      margin-bottom: 1rem !important;
    }

    .swagger-ui .opblock.opblock-get {
      border-left: 4px solid #50fa7b !important;
    }

    .swagger-ui .opblock.opblock-post {
      border-left: 4px solid #ffb86c !important;
    }

    .swagger-ui .opblock .opblock-summary {
      background-color: #44475a !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui .opblock .opblock-summary-method {
      background-color: #50fa7b !important;
      color: #282a36 !important;
      font-weight: bold !important;
    }

    .swagger-ui .opblock.opblock-post .opblock-summary-method {
      background-color: #ffb86c !important;
    }

    .swagger-ui .opblock .opblock-summary-path {
      color: #8be9fd !important;
      font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace !important;
    }

    .swagger-ui .opblock .opblock-summary-description {
      color: #f8f8f2 !important;
    }

    .swagger-ui .opblock-section {
      background-color: #282a36 !important;
    }

    .swagger-ui .opblock-section-header {
      background-color: #44475a !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui .parameters-container {
      background-color: #282a36 !important;
    }

    .swagger-ui .parameter__name {
      color: #8be9fd !important;
      font-weight: bold !important;
    }

    .swagger-ui .parameter__type {
      color: #bd93f9 !important;
    }

    .swagger-ui .parameter__in {
      color: #6272a4 !important;
    }

    .swagger-ui .parameter__description {
      color: #f8f8f2 !important;
    }

    .swagger-ui .btn {
      background-color: #bd93f9 !important;
      border: 1px solid #bd93f9 !important;
      color: #282a36 !important;
      border-radius: 4px !important;
    }

    .swagger-ui .btn:hover {
      background-color: #ff79c6 !important;
      border-color: #ff79c6 !important;
    }

    .swagger-ui .btn.execute {
      background-color: #50fa7b !important;
      border: 1px solid #50fa7b !important;
      color: #282a36 !important;
    }

    .swagger-ui .btn.execute:hover {
      background-color: #8be9fd !important;
      border-color: #8be9fd !important;
    }

    .swagger-ui input, 
    .swagger-ui textarea, 
    .swagger-ui select {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      color: #f8f8f2 !important;
      border-radius: 4px !important;
    }

    .swagger-ui input:focus,
    .swagger-ui textarea:focus,
    .swagger-ui select:focus {
      border-color: #8be9fd !important;
      box-shadow: 0 0 0 2px rgba(139, 233, 253, 0.3) !important;
    }

    .swagger-ui .response {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
    }

    .swagger-ui .response-col_status {
      color: #50fa7b !important;
      font-weight: bold !important;
    }

    .swagger-ui .response-col_description {
      color: #f8f8f2 !important;
    }

    .swagger-ui .highlight-code,
    .swagger-ui pre {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui .model-container {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
    }

    .swagger-ui .model .property {
      color: #8be9fd !important;
    }

    .swagger-ui .model .type {
      color: #bd93f9 !important;
    }

    .swagger-ui .topbar {
      background-color: #44475a !important;
      border-bottom: 2px solid #bd93f9 !important;
    }

    .swagger-ui .topbar .download-url-wrapper {
      display: none !important;
    }

    .swagger-ui table {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
    }

    .swagger-ui table thead tr th {
      background-color: #6272a4 !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui table tbody tr td {
      color: #f8f8f2 !important;
      border-bottom: 1px solid #6272a4 !important;
    }

    .swagger-ui .servers select {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
      color: #f8f8f2 !important;
    }

    .swagger-ui .loading-container {
      background-color: #282a36 !important;
    }

    .swagger-ui .errors-wrapper {
      background-color: rgba(255, 85, 85, 0.1) !important;
      border: 1px solid #ff5555 !important;
      color: #ff5555 !important;
    }

    /* Additional overrides for stubborn elements */
    .swagger-ui * {
      box-sizing: border-box;
    }

    .swagger-ui .swagger-ui-wrap {
      background-color: #282a36 !important;
    }

    .swagger-ui section.models {
      background-color: #282a36 !important;
    }

    .swagger-ui section.models h4 {
      color: #bd93f9 !important;
    }

    .swagger-ui .model-box {
      background-color: #44475a !important;
      border: 1px solid #6272a4 !important;
    }

    .swagger-ui .model-title {
      color: #8be9fd !important;
    }

    .swagger-ui .prop-type {
      color: #bd93f9 !important;
    }

    .swagger-ui .prop-format {
      color: #6272a4 !important;
    }

    .swagger-ui .renderedMarkdown p {
      color: #f8f8f2 !important;
    }

    .swagger-ui .renderedMarkdown h1,
    .swagger-ui .renderedMarkdown h2,
    .swagger-ui .renderedMarkdown h3 {
      color: #bd93f9 !important;
    }

    .swagger-ui .markdown p {
      color: #f8f8f2 !important;
    }

    .swagger-ui .parameter-controls {
      background-color: #44475a !important;
    }

    .swagger-ui .responses-inner h4,
    .swagger-ui .responses-inner h5 {
      color: #8be9fd !important;
    }

    .swagger-ui .response-content-type {
      color: #6272a4 !important;
    }

    .swagger-ui .copy-to-clipboard {
      background-color: #bd93f9 !important;
      border-color: #bd93f9 !important;
      color: #282a36 !important;
    }

    /* Force all backgrounds to be dark */
    .swagger-ui div,
    .swagger-ui section,
    .swagger-ui article,
    .swagger-ui header,
    .swagger-ui main {
      background-color: inherit !important;
    }
  `,
  customSiteTitle: "🌍 CityList API Documentation",
  customfavIcon: "/favicon.ico",
  swaggerOptions: {
    docExpansion: 'list',
    filter: true,
    showRequestHeaders: true,
    tryItOutEnabled: true,
  }
};

app.use('/docs', swaggerUi.serve, swaggerUi.setup(specs, swaggerUiOptions));
app.get('/api/docs', (req, res) => {
  res.json(specs);
});

const apiV1Routes = require('./routes/api/v1');
app.use('/api/v1', apiV1Routes);

// Raw data endpoint
app.get('/api/data', (req, res) => {
  try {
    const fs = require('fs');
    const filePath = path.join(__dirname, 'api/city.list.json');
    const data = fs.readFileSync(filePath, 'utf8');
    const cities = JSON.parse(data);
    
    res.setHeader('Content-Type', 'application/json');
    res.json(cities);
  } catch (error) {
    res.status(500).json({ error: 'Unable to load city data' });
  }
});

const webRoutes = require('./routes/web');
app.use('/', webRoutes);

app.listen(PORT, HOST, () => {
  console.log(`Server is running on ${HOST}:${PORT}`);
});

module.exports = app;