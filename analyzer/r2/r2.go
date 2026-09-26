// Package r2 stores rendered cards/badges in the Cloudflare R2 bucket
// (agentstats-cards). Layout (docs 04):
//
//	u/<login>/current.json          {"v": 7}
//	u/<login>/v7/badge.svg
//	u/<login>/v7/before-after.svg|png
//	u/<login>/v7/composition.svg|png
//	u/<login>/v7/og.png             1200×630
//	u/<login>/badges/<id>.svg|png   custom badges (badge builder)
//
// Like D1 (store/d1.go, doc 13 fix 1) the container does NOT use the S3 API or
// hold an R2 S3 token. It talks to the front Worker's internal route, which uses
// the R2 *binding*:
//
//	PUT    {base}/_internal/r2?key=<key>       body = bytes, Content-Type preserved
//	GET    {base}/_internal/r2?key=<key>       404 when absent
//	DELETE {base}/_internal/r2?prefix=<prefix> removes every object under prefix
//
// every request carrying X-Internal-Secret: $INTERNAL_D1_SECRET (the same shared
// secret as the D1 route). The route URL comes from INTERNAL_R2_URL, else it is
// derived from INTERNAL_D1_URL by swapping /_internal/d1 for /_internal/r2.
// When neither is set the Client is disabled and the web server renders cards
// inline from the stats JSON (local dev fallback).
package r2

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrNotFound is returned by GetObject when the key does not exist.
var ErrNotFound = errors.New("r2: object not found")

// Client talks to the Worker's internal R2 route. A nil or unconfigured Client
// is Enabled()==false and every write is a no-op.
type Client struct {
	URL    string // full route URL, e.g. https://agentstats.deemwar.com/_internal/r2
	secret string // shared secret; never logged, never in a URL
	HTTP   *http.Client
}

// New builds a Client for an explicit route URL and secret (tests, wiring).
func New(routeURL, secret string) *Client {
	return &Client{URL: routeURL, secret: secret, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// FromEnv builds a Client from the container environment. It never fails: an
// unconfigured Client simply reports Enabled()==false.
func FromEnv() *Client {
	return New(routeFromEnv(os.Getenv("INTERNAL_R2_URL"), os.Getenv("INTERNAL_D1_URL")), os.Getenv("INTERNAL_D1_SECRET"))
}

// routeFromEnv picks the explicit R2 route, else derives it from the D1 route.
func routeFromEnv(r2URL, d1URL string) string {
	if r2URL != "" {
		return r2URL
	}
	if d1URL == "" {
		return ""
	}
	if strings.HasSuffix(d1URL, "/_internal/d1") {
		return strings.TrimSuffix(d1URL, "/_internal/d1") + "/_internal/r2"
	}
	u, err := url.Parse(d1URL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/_internal/r2"
}

// Enabled reports whether the internal route and secret are configured.
func (c *Client) Enabled() bool {
	return c != nil && c.URL != "" && c.secret != ""
}

// Object is one artifact to upload.
type Object struct {
	Key         string // e.g. u/muthuishere/v7/badge.svg
	Body        []byte
	ContentType string
}

// VersionKey builds a versioned artifact key: u/<login>/v<n>/<name>.
func VersionKey(login string, version int, name string) string {
	return fmt.Sprintf("u/%s/v%d/%s", strings.ToLower(login), version, name)
}

// VersionPrefix is the prefix of one version: u/<login>/v<n>/ (trailing slash so
// v1 never matches v10).
func VersionPrefix(login string, version int) string {
	return fmt.Sprintf("u/%s/v%d/", strings.ToLower(login), version)
}

// BadgeKey builds a custom-badge key: u/<login>/badges/<id>.<ext>.
func BadgeKey(login, badgeID, ext string) string {
	return fmt.Sprintf("u/%s/badges/%s.%s", strings.ToLower(login), badgeID, ext)
}

// PointerKey is the current-version pointer key.
func PointerKey(login string) string {
	return fmt.Sprintf("u/%s/current.json", strings.ToLower(login))
}

// PrefixKey is the per-user prefix (delete-my-data removes everything under it).
func PrefixKey(login string) string {
	return fmt.Sprintf("u/%s/", strings.ToLower(login))
}

// PutAll uploads every object, then flips the current.json pointer to `version`
// LAST so a reader never sees a half-written version (docs 02/03). It is a
// no-op when the client is unconfigured.
func (c *Client) PutAll(login string, version int, objs []Object) error {
	if !c.Enabled() {
		return nil
	}
	for _, o := range objs {
		if err := c.PutObject(o.Key, o.Body, o.ContentType); err != nil {
			return err
		}
	}
	ptr := []byte(fmt.Sprintf(`{"v":%d}`, version))
	return c.PutObject(PointerKey(login), ptr, "application/json")
}

// PruneVersions deletes versions older than version-1 (keeps the current and
// the previous one, so a reader holding the old pointer still resolves). Each
// successful job prunes, so only the last few older versions can still exist.
func (c *Client) PruneVersions(login string, version int) error {
	if !c.Enabled() {
		return nil
	}
	var errs []error
	for v := version - 2; v >= 1 && v >= version-5; v-- {
		if err := c.deletePrefix(VersionPrefix(login, v)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Put uploads a single object (badge builder save path). No-op when disabled.
func (c *Client) Put(key string, body []byte, contentType string) error {
	if !c.Enabled() {
		return nil
	}
	return c.PutObject(key, body, contentType)
}

// DeletePrefix removes every object under u/<login>/ (delete-my-data, docs 07).
func (c *Client) DeletePrefix(login string) error {
	if !c.Enabled() {
		return nil
	}
	if strings.TrimSpace(login) == "" {
		return errors.New("r2: refusing to delete an empty user prefix")
	}
	return c.deletePrefix(PrefixKey(login))
}

// PutObject stores body under key with the given Content-Type.
func (c *Client) PutObject(key string, body []byte, contentType string) error {
	if !c.Enabled() {
		return errors.New("r2: not configured")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	resp, err := c.do(http.MethodPut, "key", key, bytes.NewReader(body), contentType)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("r2 put %s: status %d", key, resp.StatusCode)
	}
	return nil
}

// GetObject fetches key. It returns ErrNotFound when the object is absent.
func (c *Client) GetObject(key string) ([]byte, string, error) {
	if !c.Enabled() {
		return nil, "", errors.New("r2: not configured")
	}
	resp, err := c.do(http.MethodGet, "key", key, nil, "")
	if err != nil {
		return nil, "", err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("r2 get %s: status %d", key, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header.Get("Content-Type"), err
}

func (c *Client) deletePrefix(prefix string) error {
	if prefix == "" || prefix == "u/" || !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("r2: refusing unsafe delete prefix %q", prefix)
	}
	resp, err := c.do(http.MethodDelete, "prefix", prefix, nil, "")
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("r2 delete prefix %s: status %d", prefix, resp.StatusCode)
	}
	return nil
}

func (c *Client) do(method, param, value string, body io.Reader, contentType string) (*http.Response, error) {
	u := c.URL + "?" + url.Values{param: {value}}.Encode()
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Shared secret from env: header only, never logged, never in the URL.
	req.Header.Set("X-Internal-Secret", c.secret)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
}
