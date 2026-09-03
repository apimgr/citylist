const express = require('express');
const router = express.Router();
const fs = require('fs');
const path = require('path');

router.get('/', async (req, res) => {
  try {
    const cityCount = await getCityCount();
    res.render('index', { 
      title: 'Home',
      description: 'CityList API - Worldwide city database with geographic coordinates',
      cityCount: cityCount.toLocaleString()
    });
  } catch (error) {
    res.render('index', { 
      title: 'Home',
      description: 'CityList API - Worldwide city database with geographic coordinates'
    });
  }
});

router.get('/search', (req, res) => {
  res.render('search', { 
    title: 'Search Cities',
    description: 'Search through our worldwide city database'
  });
});

router.get('/coordinates', (req, res) => {
  res.render('coordinates', { 
    title: 'Find Nearest City',
    description: 'Find the closest city to any coordinates using your location or manual input'
  });
});

async function getCityCount() {
  try {
    const filePath = path.join(__dirname, '../api/city.list.json');
    const data = fs.readFileSync(filePath, 'utf8');
    const cities = JSON.parse(data);
    return cities.length;
  } catch (error) {
    return 200000;
  }
}

module.exports = router;