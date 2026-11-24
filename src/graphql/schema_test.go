package graphql

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/zipcodes"
)

func TestSchemaDescriptionsUseRequestedLanguage(t *testing.T) {
	query := `{ __type(name: "Query") { description fields { name description args { name description } } } }`
	for _, lang := range []string{"en", "es"} {
		schema := NewSchemaForLanguage(lang, nil)
		result := schema.Exec(context.Background(), query, "", nil)
		if len(result.Errors) != 0 {
			t.Fatalf("%s introspection errors = %#v", lang, result.Errors)
		}
		response := string(result.Data)
		for _, key := range []string{"types.Query", "queries.cities", "arguments.search"} {
			want := i18n.Translate(lang, "graphql.schema."+key)
			if !strings.Contains(response, want) {
				t.Errorf("%s introspection missing %q (%s) in %s", lang, want, key, response)
			}
		}
	}
}

func TestReadOnlyCityAndZipQueries(t *testing.T) {
	cityService, err := cities.NewService([]byte(`[{"id":1,"name":"London","country":"GB","coord":{"lat":51.5,"lon":-0.1}}]`))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("US.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("US\t10001\tNew York\tNew York\tNY\tNew York\t061\t\t\t40.75\t-73.99\t4\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	postal, err := zipcodes.NewService(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	schema := NewSchema(cityService, postal)
	response := schema.Exec(context.Background(), `{ cityByID(id: 1) { id name country coord { lat lon } } zipcode(zip: "10001") { zip city state lat lon } zipcodes(search: "York", state: "NY") { zip } }`, "", nil)
	if len(response.Errors) != 0 {
		t.Fatalf("query errors = %#v", response.Errors)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["cityByID"] == nil || data["zipcode"] == nil || data["zipcodes"] == nil {
		t.Fatalf("query result missing expected data: %s", response.Data)
	}
	queries := []string{
		`{ cities(search: "lon", country: "gb", limit: 1) { name country } nearestCity(lat: 51.5, lon: -0.1) { id } }`,
		`{ cities(limit: 1) { name } cityByID(id: 999) { id } zipcode(zip: "00000") { zip } }`,
	}
	for _, query := range queries {
		if result := schema.Exec(context.Background(), query, "", nil); len(result.Errors) != 0 {
			t.Fatalf("query %q errors = %#v", query, result.Errors)
		}
	}
	withoutPostal := NewSchema(cityService)
	if result := withoutPostal.Exec(context.Background(), `{ zipcode(zip: "10001") { zip } zipcodes(search: "York") { zip } }`, "", nil); len(result.Errors) != 0 {
		t.Fatalf("optional postal query errors = %#v", result.Errors)
	}
	if len(schema.Exec(context.Background(), `mutation { deleteCity(id: 1) }`, "", nil).Errors) == 0 {
		t.Fatal("schema should not expose mutations")
	}
}
