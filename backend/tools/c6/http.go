package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// client is one browser: its own cookies, CSRF token and anonymous client id.
type client struct {
	base     string
	h        *http.Client
	csrf     string
	clientID string
}

func newClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{base: strings.TrimRight(base, "/"), h: &http.Client{Jar: jar, Timeout: 30 * time.Second}, clientID: uuid()}
}

type response struct {
	status int
	body   map[string]any
	raw    []byte
	header http.Header
}

func (r response) code() string {
	if e, ok := r.body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

// do sends a request like the app does: same Origin, JSON body, the client's CSRF token.
func (c *client) do(method, path string, body any, headers ...string) response {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return response{status: -1}
	}
	req.Header.Set("Origin", c.base)
	req.Header.Set("X-Client-Id", c.clientID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return response{status: -1, raw: []byte(err.Error())}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, raw: raw, header: resp.Header}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// cookie returns the value of a cookie the client holds for the base URL.
func (c *client) cookie(name string) string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.h.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// setCookie gives the client a cookie (a session carried over from another binary).
func (c *client) setCookie(name, value string) {
	u, _ := url.Parse(c.base)
	c.h.Jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

func randomDigits(n int) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	s := ""
	for i := 0; i < n; i++ {
		s += string(rune('0' + int(b[i])%10))
	}
	return s
}

func randomSecret() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "c6-" + hex.EncodeToString(b[:])
}

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	}
	return -1
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func items(r response) []map[string]any {
	var out []map[string]any
	list, _ := r.body["items"].([]any)
	for _, it := range list {
		out = append(out, obj(it))
	}
	return out
}
