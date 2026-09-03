## CitiesList

json files with a list of all cities worldwide(id, city, country, geo coordinates). Good for city search functionality, e.g.: weather for city functionality.

City ids are from geonames database ( *<http://www.geonames.org>* ). More about geonames db: *<https://en.wikipedia.org/wiki/GeoNames>*.

City ids also match the ids from openweathermap service (see *<https://openweathermap.org/find?q=>* ).

```shell
curl -q -LSsf 'https://raw.githubusercontent.com/apimgr/CityList/master/api/city.list.json' | jq -r '.'
```

---

## Zipcodes

Added us.zipcode.json

```shell
curl -q -LSsf 'https://raw.githubusercontent.com/apimgr/CityList/master/api/us.zipcodes.json' | jq -r '.'
```
