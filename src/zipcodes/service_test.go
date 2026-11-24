package zipcodes

import (
	"archive/zip"
	"bytes"
	"testing"
)

func fixtureArchive(t *testing.T, rows string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("US.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(rows)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestNewServiceAndLookups(t *testing.T) {
	archive := fixtureArchive(t, "US\t02108\tBoston\tMassachusetts\tMA\tSuffolk\t025\t\t\t42.3576\t-71.0636\t4\nUS\t02108\tBoston Alternate\tMassachusetts\tMA\tSuffolk\t025\t\t\t42.3576\t-71.0636\t4\nUS\t10001\tNew York\tNew York\tNY\tNew York\t061\t\t\t40.7506\t-73.9972\t4\nUS\t09001\tAPO AA\t\t\t\t\t\t\t38.1105\t15.6613\t\n")
	service, err := NewService(archive)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if got := service.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}
	if record, ok := service.Lookup("02108"); !ok || record.City != "Boston" {
		t.Fatalf("Lookup() = %#v, %v", record, ok)
	}
	if _, ok := service.Lookup(" 10001 "); !ok {
		t.Fatal("Lookup() should trim ZIP input")
	}
	if results := service.Search("york", "ny", 10); len(results) != 1 || results[0].ZIP != "10001" {
		t.Fatalf("Search() = %#v", results)
	}
	if results, total := service.ByState("ma", 1, 1); total != 1 || len(results) != 1 {
		t.Fatalf("ByState() = %#v, %d", results, total)
	}
	nearby, err := service.Nearby(42.35, -71.06, 10, 10)
	if err != nil || len(nearby) != 1 || nearby[0].ZIP != "02108" {
		t.Fatalf("Nearby() = %#v, %v", nearby, err)
	}
	if _, err := service.Nearby(100, 0, 10, 10); err == nil {
		t.Fatal("Nearby() should reject out-of-range coordinates")
	}
}

func TestNewServiceRejectsInvalidArchiveAndRecords(t *testing.T) {
	if _, err := NewService([]byte("not a ZIP")); err == nil {
		t.Fatal("NewService() should reject invalid archives")
	}
	if _, err := NewService(fixtureArchive(t, "US\t123\tCity\tState\tCA\t\t\t\t\t1\t2\t")); err == nil {
		t.Fatal("NewService() should reject malformed records")
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if _, err := writer.Create("other.txt"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(buffer.Bytes()); err == nil {
		t.Fatal("NewService() should require US.txt")
	}
}
