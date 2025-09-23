const express = require('express');
const router = express.Router();
const fs = require('fs');
const path = require('path');

let citiesData = null;

function loadCities() {
  if (!citiesData) {
    try {
      const filePath = path.join(__dirname, '../../../api/city.list.json');
      const data = fs.readFileSync(filePath, 'utf8');
      citiesData = JSON.parse(data);
    } catch (error) {
      console.error('Error loading cities data:', error);
      citiesData = [];
    }
  }
  return citiesData;
}

function calculateDistance(lat1, lon1, lat2, lon2) {
  const R = 6371; // Earth's radius in kilometers
  const dLat = (lat2 - lat1) * Math.PI / 180;
  const dLon = (lon2 - lon1) * Math.PI / 180;
  const a = 
    Math.sin(dLat/2) * Math.sin(dLat/2) +
    Math.cos(lat1 * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180) * 
    Math.sin(dLon/2) * Math.sin(dLon/2);
  const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1-a));
  return R * c;
}

/**
 * @swagger
 * components:
 *   schemas:
 *     City:
 *       type: object
 *       properties:
 *         id:
 *           type: integer
 *           description: Unique city identifier from GeoNames
 *           example: 2643743
 *         name:
 *           type: string
 *           description: City name
 *           example: "London"
 *         country:
 *           type: string
 *           description: ISO 3166-1 alpha-2 country code
 *           example: "GB"
 *         coord:
 *           type: object
 *           properties:
 *             lon:
 *               type: number
 *               description: Longitude
 *               example: -0.12574
 *             lat:
 *               type: number
 *               description: Latitude
 *               example: 51.50853
 */

/**
 * @swagger
 * /cities:
 *   get:
 *     summary: Get all cities (paginated)
 *     tags: [Cities]
 *     parameters:
 *       - in: query
 *         name: page
 *         schema:
 *           type: integer
 *           default: 1
 *         description: Page number
 *       - in: query
 *         name: limit
 *         schema:
 *           type: integer
 *           default: 50
 *           maximum: 1000
 *         description: Number of cities per page
 *     responses:
 *       200:
 *         description: List of cities
 *         content:
 *           application/json:
 *             schema:
 *               type: object
 *               properties:
 *                 cities:
 *                   type: array
 *                   items:
 *                     $ref: '#/components/schemas/City'
 *                 pagination:
 *                   type: object
 *                   properties:
 *                     page:
 *                       type: integer
 *                     limit:
 *                       type: integer
 *                     total:
 *                       type: integer
 *                     pages:
 *                       type: integer
 */
router.get('/', (req, res) => {
  const cities = loadCities();
  const page = parseInt(req.query.page) || 1;
  const limit = Math.min(parseInt(req.query.limit) || 50, 1000);
  const startIndex = (page - 1) * limit;
  const endIndex = startIndex + limit;

  const paginatedCities = cities.slice(startIndex, endIndex);
  const totalPages = Math.ceil(cities.length / limit);

  res.json({
    cities: paginatedCities,
    pagination: {
      page,
      limit,
      total: cities.length,
      pages: totalPages
    }
  });
});

/**
 * @swagger
 * /cities/search:
 *   get:
 *     summary: Search cities by name
 *     tags: [Cities]
 *     parameters:
 *       - in: query
 *         name: q
 *         required: true
 *         schema:
 *           type: string
 *         description: Search query (city name)
 *       - in: query
 *         name: limit
 *         schema:
 *           type: integer
 *           default: 20
 *           maximum: 100
 *         description: Number of results to return
 *     responses:
 *       200:
 *         description: Search results
 *         content:
 *           application/json:
 *             schema:
 *               type: array
 *               items:
 *                 $ref: '#/components/schemas/City'
 *       400:
 *         description: Invalid search query
 */
router.get('/search', (req, res) => {
  const cities = loadCities();
  const query = req.query.q;
  const limit = Math.min(parseInt(req.query.limit) || 20, 100);

  if (!query || query.length < 2) {
    return res.status(400).json({ error: 'Search query must be at least 2 characters' });
  }

  const searchQuery = query.toLowerCase();
  const results = cities
    .filter(city => city.name.toLowerCase().includes(searchQuery))
    .slice(0, limit);

  res.json(results);
});

/**
 * @swagger
 * /cities/coordinates:
 *   get:
 *     summary: Find closest city by coordinates (GET)
 *     tags: [Cities]
 *     parameters:
 *       - in: query
 *         name: longitude
 *         required: true
 *         schema:
 *           type: number
 *         description: Longitude coordinate
 *         example: -0.12574
 *       - in: query
 *         name: latitude
 *         required: true
 *         schema:
 *           type: number
 *         description: Latitude coordinate
 *         example: 51.50853
 *     responses:
 *       200:
 *         description: Closest city to the coordinates
 *         content:
 *           application/json:
 *             schema:
 *               type: object
 *               properties:
 *                 city:
 *                   $ref: '#/components/schemas/City'
 *                 distance:
 *                   type: number
 *                   description: Distance to the city in kilometers
 *                   example: 2.5
 *       400:
 *         description: Invalid coordinates
 *   post:
 *     summary: Find closest city by coordinates (POST)
 *     tags: [Cities]
 *     requestBody:
 *       required: true
 *       content:
 *         application/json:
 *           schema:
 *             type: object
 *             required:
 *               - longitude
 *               - latitude
 *             properties:
 *               longitude:
 *                 type: number
 *                 description: Longitude coordinate
 *                 example: -0.12574
 *               latitude:
 *                 type: number
 *                 description: Latitude coordinate
 *                 example: 51.50853
 *     responses:
 *       200:
 *         description: Closest city to the coordinates
 *         content:
 *           application/json:
 *             schema:
 *               type: object
 *               properties:
 *                 city:
 *                   $ref: '#/components/schemas/City'
 *                 distance:
 *                   type: number
 *                   description: Distance to the city in kilometers
 *                   example: 2.5
 *       400:
 *         description: Invalid coordinates
 */
router.get('/coordinates', (req, res) => {
  const longitude = parseFloat(req.query.longitude);
  const latitude = parseFloat(req.query.latitude);

  if (isNaN(longitude) || isNaN(latitude)) {
    return res.status(400).json({ error: 'Both longitude and latitude must be valid numbers' });
  }

  if (longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90) {
    return res.status(400).json({ error: 'Invalid coordinates: longitude must be between -180 and 180, latitude between -90 and 90' });
  }

  const cities = loadCities();
  let closestCity = null;
  let minDistance = Infinity;

  for (const city of cities) {
    const distance = calculateDistance(latitude, longitude, city.coord.lat, city.coord.lon);
    if (distance < minDistance) {
      minDistance = distance;
      closestCity = city;
    }
  }

  if (closestCity) {
    res.json({
      city: closestCity,
      distance: Math.round(minDistance * 100) / 100 // Round to 2 decimal places
    });
  } else {
    res.status(404).json({ error: 'No cities found' });
  }
});

router.post('/coordinates', (req, res) => {
  const { longitude, latitude } = req.body;

  if (longitude === undefined || latitude === undefined) {
    return res.status(400).json({ error: 'Both longitude and latitude are required in request body' });
  }

  const lon = parseFloat(longitude);
  const lat = parseFloat(latitude);

  if (isNaN(lon) || isNaN(lat)) {
    return res.status(400).json({ error: 'Both longitude and latitude must be valid numbers' });
  }

  if (lon < -180 || lon > 180 || lat < -90 || lat > 90) {
    return res.status(400).json({ error: 'Invalid coordinates: longitude must be between -180 and 180, latitude between -90 and 90' });
  }

  const cities = loadCities();
  let closestCity = null;
  let minDistance = Infinity;

  for (const city of cities) {
    const distance = calculateDistance(lat, lon, city.coord.lat, city.coord.lon);
    if (distance < minDistance) {
      minDistance = distance;
      closestCity = city;
    }
  }

  if (closestCity) {
    res.json({
      city: closestCity,
      distance: Math.round(minDistance * 100) / 100 // Round to 2 decimal places
    });
  } else {
    res.status(404).json({ error: 'No cities found' });
  }
});

/**
 * @swagger
 * /cities/{id}:
 *   get:
 *     summary: Get city by ID
 *     tags: [Cities]
 *     parameters:
 *       - in: path
 *         name: id
 *         required: true
 *         schema:
 *           type: integer
 *         description: City ID
 *     responses:
 *       200:
 *         description: City details
 *         content:
 *           application/json:
 *             schema:
 *               $ref: '#/components/schemas/City'
 *       404:
 *         description: City not found
 */
router.get('/:id', (req, res) => {
  const cities = loadCities();
  const cityId = parseInt(req.params.id);
  const city = cities.find(c => c.id === cityId);

  if (!city) {
    return res.status(404).json({ error: 'City not found' });
  }

  res.json(city);
});

/**
 * @swagger
 * /cities/country/{code}:
 *   get:
 *     summary: Get cities by country code
 *     tags: [Cities]
 *     parameters:
 *       - in: path
 *         name: code
 *         required: true
 *         schema:
 *           type: string
 *         description: ISO 3166-1 alpha-2 country code (e.g., US, GB, JP)
 *       - in: query
 *         name: limit
 *         schema:
 *           type: integer
 *           default: 50
 *           maximum: 1000
 *         description: Number of cities to return
 *     responses:
 *       200:
 *         description: Cities in the specified country
 *         content:
 *           application/json:
 *             schema:
 *               type: array
 *               items:
 *                 $ref: '#/components/schemas/City'
 *       400:
 *         description: Invalid country code
 */
router.get('/country/:code', (req, res) => {
  const cities = loadCities();
  const countryCode = req.params.code.toUpperCase();
  const limit = Math.min(parseInt(req.query.limit) || 50, 1000);

  if (countryCode.length !== 2) {
    return res.status(400).json({ error: 'Country code must be 2 characters (ISO 3166-1 alpha-2)' });
  }

  const results = cities
    .filter(city => city.country === countryCode)
    .slice(0, limit);

  res.json(results);
});

module.exports = router;