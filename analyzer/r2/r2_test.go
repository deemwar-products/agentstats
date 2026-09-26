package r2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

const fakeSecret = "FAKE-INTERNAL-SECRET-FOR-TESTS"

// fakeRoute emulates the front Worker's /_internal/r2 route over an in-memory bucket.
type fakeRoute struct {
	mu   sync.Mutex
	objs map[string]string // key -> body
	ct   map[string]string
}

func (f *fakeRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/_internal/r2" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("X-Internal-Secret") != fakeSecret {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key, prefix := r.URL.Query().Get("key"), r.URL.Query().Get("prefix")
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objs[key], f.ct[key] = string(b), r.Header.Get("Content-Type")
	case http.MethodGet:
		b, ok := f.objs[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f.ct[key])
		io.WriteString(w, b)
	case http.MethodDelete:
		if prefix == "" {
			http.Error(w, "prefix required", http.StatusBadRequest)
			return
		}
		for k := range f.objs {
			if strings.HasPrefix(k, prefix) {
				delete(f.objs, k)
			}
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeRoute) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ks []string
	for k := range f.objs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func setup(t *testing.T) (*Client, *fakeRoute) {
	f := &fakeRoute{objs: map[string]string{}, ct: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/_internal/r2", fakeSecret), f
}

func TestPutGetDelete(t *testing.T) {
	c, f := setup(t)
	if err := c.PutObject("u/octo/v1/badge.svg", []byte("<svg/>"), "image/svg+xml"); err != nil {
		t.Fatal(err)
	}
	b, ct, err := c.GetObject("u/octo/v1/badge.svg")
	if err != nil || string(b) != "<svg/>" || ct != "image/svg+xml" {
		t.Fatalf("get = %q %q %v", b, ct, err)
	}
	if _, _, err := c.GetObject("u/octo/nope"); err != ErrNotFound {
		t.Fatalf("missing key err = %v", err)
	}
	_ = c.PutObject("u/octocat/v1/badge.svg", []byte("x"), "")
	if err := c.DeletePrefix("OCTO"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.keys(), ","); got != "u/octocat/v1/badge.svg" {
		t.Fatalf("after delete: %s", got)
	}
	if err := c.DeletePrefix(""); err == nil {
		t.Fatal("empty login must be refused")
	}
}

func TestPutAllFlipsPointerAndPrunes(t *testing.T) {
	c, f := setup(t)
	for v := 1; v <= 4; v++ {
		objs := []Object{
			{Key: VersionKey("Octo", v, "badge.svg"), Body: []byte("b"), ContentType: "image/svg+xml"},
			{Key: VersionKey("Octo", v, "og.png"), Body: []byte("p"), ContentType: "image/png"},
		}
		if err := c.PutAll("Octo", v, objs); err != nil {
			t.Fatal(err)
		}
		if err := c.PruneVersions("Octo", v); err != nil {
			t.Fatal(err)
		}
	}
	// v10 must never be caught by a v1 prefix.
	_ = c.PutObject("u/octo/v10/badge.svg", []byte("x"), "")
	_ = c.PruneVersions("octo", 4)
	want := "u/octo/current.json,u/octo/v10/badge.svg,u/octo/v3/badge.svg,u/octo/v3/og.png,u/octo/v4/badge.svg,u/octo/v4/og.png"
	if got := strings.Join(f.keys(), ","); got != want {
		t.Fatalf("keys =\n %s\nwant\n %s", got, want)
	}
	b, ct, _ := c.GetObject(PointerKey("octo"))
	if string(b) != `{"v":4}` || ct != "application/json" {
		t.Fatalf("pointer = %s (%s)", b, ct)
	}
}

func TestWrongSecretFails(t *testing.T) {
	c, _ := setup(t)
	c.secret = "wrong"
	if err := c.PutObject("u/x/v1/a", []byte("a"), ""); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledIsNoop(t *testing.T) {
	var nilc *Client
	if nilc.Enabled() || nilc.PutAll("x", 1, nil) != nil || nilc.DeletePrefix("x") != nil {
		t.Fatal("nil client must be a disabled no-op")
	}
	c := New("", "")
	if c.Enabled() || c.Put("k", nil, "") != nil {
		t.Fatal("unconfigured client must be a no-op")
	}
}

func TestRouteFromEnv(t *testing.T) {
	cases := [][3]string{
		{"", "https://w.example.test/_internal/d1", "https://w.example.test/_internal/r2"},
		{"", "https://w.example.test/other", "https://w.example.test/_internal/r2"},
		{"https://r.example.test/_internal/r2", "https://w.example.test/_internal/d1", "https://r.example.test/_internal/r2"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := routeFromEnv(c[0], c[1]); got != c[2] {
			t.Errorf("routeFromEnv(%q,%q) = %q want %q", c[0], c[1], got, c[2])
		}
	}
}
