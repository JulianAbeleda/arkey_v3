package moonbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalog(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[{"id":"a"}]}`)) }))
	defer s.Close()
	c, e := Client{BaseURL: s.URL}.Catalog(context.Background())
	if e != nil || !c.HasRoute("a") {
		t.Fatal(c, e)
	}
}
