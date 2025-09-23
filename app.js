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
app.use('/docs', swaggerUi.serve, swaggerUi.setup(specs));
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

app.listen(PORT, () => {
  console.log(`Server is running on port ${PORT}`);
});

module.exports = app;