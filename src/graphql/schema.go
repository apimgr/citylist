package graphql

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/zipcodes"
	graphqlgophers "github.com/graph-gophers/graphql-go"
)

func schemaDescription(lang, key string) string {
	value, _ := json.Marshal(i18n.Translate(lang, "graphql.schema."+key))
	return string(value)
}

func schemaSDL(lang string) string {
	d := func(key string) string { return schemaDescription(lang, key) }
	return fmt.Sprintf(`
%s
type Query {
  %s
  cities(
    %s
    search: String,
    %s
    country: String,
    %s
    limit: Int
  ): [City!]!
  %s
  cityByID(
    %s
    id: Int!
  ): City
  %s
  nearestCity(
    %s
    lat: Float!,
    %s
    lon: Float!
  ): City
  %s
  zipcode(
    %s
    zip: String!
  ): Zipcode
  %s
  zipcodes(
    %s
    search: String!,
    %s
    state: String,
    %s
    limit: Int
  ): [Zipcode!]!
}
%s
type City {
  %s
  id: Int!
  %s
  name: String!
  %s
  country: String!
  %s
  coord: Coordinates!
}
%s
type Zipcode {
  %s
  zip: String!
  %s
  city: String!
  %s
  state: String!
  %s
  lat: Float!
  %s
  lon: Float!
}
%s
type Coordinates {
  %s
  lat: Float!
  %s
  lon: Float!
}
`,
		d("types.Query"), d("queries.cities"), d("arguments.search"), d("arguments.country"), d("arguments.limit"),
		d("queries.cityByID"), d("arguments.id"), d("queries.nearestCity"), d("arguments.lat"), d("arguments.lon"),
		d("queries.zipcode"), d("arguments.zip"), d("queries.zipcodes"), d("arguments.search"), d("arguments.state"), d("arguments.limit"),
		d("types.City"), d("fields.city_id"), d("fields.city_name"), d("fields.city_country"), d("fields.city_coord"),
		d("types.Zipcode"), d("fields.zipcode_zip"), d("fields.zipcode_city"), d("fields.zipcode_state"), d("fields.zipcode_lat"), d("fields.zipcode_lon"),
		d("types.Coordinates"), d("fields.coordinates_lat"), d("fields.coordinates_lon"))
}

// NewSchema creates the read-only city query schema.
func NewSchema(service *cities.Service, postal ...*zipcodes.Service) *graphqlgophers.Schema {
	return NewSchemaForLanguage("en", service, postal...)
}

// NewSchemaForLanguage creates a read-only city query schema with localized descriptions.
func NewSchemaForLanguage(lang string, service *cities.Service, postal ...*zipcodes.Service) *graphqlgophers.Schema {
	var postalService *zipcodes.Service
	if len(postal) > 0 {
		postalService = postal[0]
	}
	return graphqlgophers.MustParseSchema(schemaSDL(lang), &queryResolver{service: service, postal: postalService}, graphqlgophers.MaxDepth(8), graphqlgophers.UseStringDescriptions())
}

type queryResolver struct {
	service *cities.Service
	postal  *zipcodes.Service
}

func (r *queryResolver) Cities(args struct {
	Search  *string
	Country *string
	Limit   *int32
}) []*cityResolver {
	limit := 50
	if args.Limit != nil {
		limit = int(*args.Limit)
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	var records []cities.City
	if args.Search == nil && args.Country == nil {
		records, _ = r.service.GetAll(1, limit)
	} else {
		query := ""
		if args.Search != nil {
			query = strings.ToLower(strings.TrimSpace(*args.Search))
		}
		country := ""
		if args.Country != nil {
			country = strings.ToUpper(strings.TrimSpace(*args.Country))
		}
		for _, record := range r.service.GetRaw() {
			if query != "" && (len(query) < 2 || !strings.Contains(strings.ToLower(record.Name), query)) {
				continue
			}
			if country != "" && !strings.EqualFold(record.Country, country) {
				continue
			}
			records = append(records, record)
			if len(records) >= limit {
				break
			}
		}
	}
	if len(records) > limit {
		records = records[:limit]
	}
	result := make([]*cityResolver, len(records))
	for i := range records {
		city := records[i]
		result[i] = &cityResolver{city: city}
	}
	return result
}

func (r *queryResolver) CityByID(args struct{ ID int32 }) *cityResolver {
	city, ok := r.service.GetByID(int(args.ID))
	if !ok {
		return nil
	}
	return &cityResolver{city: *city}
}

func (r *queryResolver) NearestCity(args struct {
	Lat float64
	Lon float64
}) *cityResolver {
	nearest, ok := r.service.FindNearest(args.Lat, args.Lon)
	if !ok {
		return nil
	}
	return &cityResolver{city: nearest.City}
}

func (r *queryResolver) Zipcode(args struct{ Zip string }) *zipcodeResolver {
	if r.postal == nil {
		return nil
	}
	record, ok := r.postal.Lookup(args.Zip)
	if !ok {
		return nil
	}
	return &zipcodeResolver{record: record}
}

func (r *queryResolver) Zipcodes(args struct {
	Search string
	State  *string
	Limit  *int32
}) []*zipcodeResolver {
	if r.postal == nil {
		return []*zipcodeResolver{}
	}
	limit := 20
	if args.Limit != nil {
		limit = int(*args.Limit)
	}
	state := ""
	if args.State != nil {
		state = *args.State
	}
	records := r.postal.Search(args.Search, state, limit)
	result := make([]*zipcodeResolver, len(records))
	for i, record := range records {
		result[i] = &zipcodeResolver{record: record}
	}
	return result
}

type zipcodeResolver struct{ record zipcodes.Record }

func (r *zipcodeResolver) Zip() string   { return r.record.ZIP }
func (r *zipcodeResolver) City() string  { return r.record.City }
func (r *zipcodeResolver) State() string { return r.record.State }
func (r *zipcodeResolver) Lat() float64  { return r.record.Lat }
func (r *zipcodeResolver) Lon() float64  { return r.record.Lon }

type cityResolver struct {
	city cities.City
}

func (r *cityResolver) ID() int32       { return int32(r.city.ID) }
func (r *cityResolver) Name() string    { return r.city.Name }
func (r *cityResolver) Country() string { return r.city.Country }
func (r *cityResolver) Coord() *coordinatesResolver {
	return &coordinatesResolver{coord: r.city.Coord}
}

type coordinatesResolver struct {
	coord cities.Coord
}

func (r *coordinatesResolver) Lat() float64 { return r.coord.Lat }
func (r *coordinatesResolver) Lon() float64 { return r.coord.Lon }
