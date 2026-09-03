const express = require('express');
const router = express.Router();

const citiesRouter = require('./cities');

router.use('/cities', citiesRouter);

router.get('/', (req, res) => {
  res.json({
    message: 'CityList API v1',
    version: '1.0.0',
    endpoints: {
      cities: '/api/v1/cities',
      search: '/api/v1/cities/search',
      coordinates: '/api/v1/cities/coordinates',
      country: '/api/v1/cities/country/:code',
      data: '/api/data',
      docs: '/docs'
    }
  });
});

module.exports = router;