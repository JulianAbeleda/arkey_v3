package moonbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}
type Model struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Model string `json:"model"`
}
type Catalog struct{ Models []Model }

func (c Client) Catalog(ctx context.Context) (Catalog, error) {
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: time.Second}
	}
	base := strings.TrimRight(c.BaseURL, "/")
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if e != nil {
		return Catalog{}, e
	}
	res, e := h.Do(r)
	if e != nil {
		return Catalog{}, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Catalog{}, fmt.Errorf("moonbridge status %s", res.Status)
	}
	var raw struct {
		Data   []Model `json:"data"`
		Models []Model `json:"models"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&raw); e != nil {
		return Catalog{}, e
	}
	if len(raw.Data) > 0 {
		return Catalog{raw.Data}, nil
	}
	return Catalog{raw.Models}, nil
}
func (c Catalog) HasRoute(route string) bool {
	for _, m := range c.Models {
		if m.ID == route || m.Name == route || m.Model == route || m.Slug == route || strings.HasSuffix(m.Slug, "/"+route) {
			return true
		}
	}
	return false
}

type State string

const (
	Online      State = "online"
	Standby     State = "standby"
	Unavailable State = "unavailable"
)

type Status struct {
	State   State
	Catalog Catalog
	Err     error
}

func (c Client) Status(ctx context.Context, binary, config string) Status {
	cat, e := c.Catalog(ctx)
	if e == nil {
		return Status{State: Online, Catalog: cat}
	}
	if binary == "" || config == "" || !executable(binary) || !regular(config) {
		return Status{State: Unavailable, Err: e}
	}
	return Status{State: Standby, Err: e}
}

func executable(path string) bool {
	i, err := os.Stat(path)
	return err == nil && i.Mode().IsRegular() && i.Mode().Perm()&0111 != 0
}
func regular(path string) bool { i, err := os.Stat(path); return err == nil && i.Mode().IsRegular() }
