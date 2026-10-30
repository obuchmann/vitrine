// Package client talks to the vitrine HTTP API as an agent. vitrine mcp uses
// it, so the token stays in this process and never reaches the agent
// (docs/adr/0002-authentication.md).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/store"
	"github.com/obuchmann/vitrine/internal/web"
)

// ErrNotVitrine means something other than vitrine answers at the URL.
var ErrNotVitrine = errors.New("not vitrine")

// Error is an error the API answered with.
type Error struct {
	Status int
	Code   string // e.g. unknown_prototype
	Hint   string
}

func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Code
	}
	return e.Code + ": " + e.Hint
}

// Client calls one vitrine.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:8720, no trailing slash
	Token   string
	HTTP    *http.Client // nil means a client without timeout; calls are bounded by their context

	// Start is called once per request when vitrine cannot be reached, e.g.
	// to start the daemon. The request is retried if it returns nil.
	Start func(context.Context) error
}

// URL is the address of a prototype page.
func (c *Client) URL(name string) string { return c.BaseURL + "/p/" + name + "/" }

// Health checks that vitrine answers at BaseURL and returns its version.
func (c *Client) Health(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/health", nil)
	if err != nil {
		return "", err
	}
	res, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var h struct{ Service, Version string }
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&h); err != nil || res.StatusCode != 200 || h.Service != "vitrine" {
		return "", fmt.Errorf("%w: %s answers with status %d", ErrNotVitrine, c.BaseURL, res.StatusCode)
	}
	return h.Version, nil
}

// PublishInput is an upload. Zero values keep what the prototype has.
type PublishInput struct {
	Name    string
	HTML    []byte
	KeepFor string
	Context map[string]string
}

// Publish uploads a new version of a prototype.
func (c *Client) Publish(ctx context.Context, in PublishInput) (web.PublishResult, error) {
	q := url.Values{}
	if in.KeepFor != "" {
		q.Set("keep_for", in.KeepFor)
	}
	if len(in.Context) > 0 {
		b, _ := json.Marshal(in.Context)
		q.Set("context", string(b))
	}
	var out web.PublishResult
	err := c.do(ctx, "POST", "/api/p/"+in.Name, q, "text/html; charset=utf-8", in.HTML, &out)
	return out, err
}

// Get describes one prototype.
func (c *Client) Get(ctx context.Context, name string) (store.Info, error) {
	var out store.Info
	err := c.do(ctx, "GET", "/api/p/"+name, nil, "", nil, &out)
	return out, err
}

// List describes every prototype.
func (c *Client) List(ctx context.Context) ([]store.Info, error) {
	var out []store.Info
	err := c.do(ctx, "GET", "/api/p", nil, "", nil, &out)
	return out, err
}

// Extend sets the minimum lifetime of a prototype.
func (c *Client) Extend(ctx context.Context, name, keepFor string) (web.PublishResult, error) {
	var out web.PublishResult
	err := c.do(ctx, "POST", "/api/p/"+name+"/extend", url.Values{"keep_for": {keepFor}}, "", nil, &out)
	return out, err
}

// Delete removes a prototype and its events.
func (c *Client) Delete(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/api/p/"+name, nil, "", nil, nil)
}

// Message sends a message to the open tabs of a prototype.
func (c *Client) Message(ctx context.Context, name, text string, data json.RawMessage) (int, error) {
	body, _ := json.Marshal(web.MessageInput{Message: text, Data: data})
	var out web.MessageResult
	err := c.do(ctx, "POST", "/api/p/"+name+"/message", nil, "application/json", body, &out)
	return out.DeliveredTo, err
}

// Reply answers a comment thread, or starts one with only a target.
func (c *Client) Reply(ctx context.Context, name string, in store.ReplyInput) (web.ReplyResult, error) {
	body, _ := json.Marshal(in)
	var out web.ReplyResult
	err := c.do(ctx, "POST", "/api/p/"+name+"/reply", nil, "application/json", body, &out)
	return out, err
}

// Status tells the open tabs what the agent is doing; empty text clears it.
func (c *Client) Status(ctx context.Context, name, text string) (int, error) {
	body, _ := json.Marshal(web.StatusInput{Text: text})
	var out web.MessageResult
	err := c.do(ctx, "POST", "/api/p/"+name+"/status", nil, "application/json", body, &out)
	return out.DeliveredTo, err
}

// Point scrolls the open tabs to an element and highlights it.
func (c *Client) Point(ctx context.Context, name, target, text string) (int, error) {
	body, _ := json.Marshal(web.PointInput{Target: target, Text: text})
	var out web.MessageResult
	err := c.do(ctx, "POST", "/api/p/"+name+"/point", nil, "application/json", body, &out)
	return out.DeliveredTo, err
}

// Events returns every stored event after since, read or not.
func (c *Client) Events(ctx context.Context, name string, types []string, since int64) ([]store.Event, error) {
	q := url.Values{"since": {strconv.FormatInt(since, 10)}}
	if len(types) > 0 {
		q.Set("types", strings.Join(types, ","))
	}
	var out []store.Event
	err := c.do(ctx, "GET", "/api/events/"+name, q, "", nil, &out)
	return out, err
}

// Next acknowledges every event up to ack and returns the unacknowledged
// events of a prototype, waiting up to wait for one to arrive.
func (c *Client) Next(ctx context.Context, name string, types []string, ack int64, wait time.Duration) (store.FetchResult, error) {
	q := url.Values{
		"name":    {name},
		"ack":     {strconv.FormatInt(ack, 10)},
		"timeout": {strconv.Itoa(int(wait / time.Second))},
	}
	if len(types) > 0 {
		q.Set("types", strings.Join(types, ","))
	}
	// The status is sent before the wait, so a prototype removed while
	// waiting is reported as an error in a 200 body.
	var out struct {
		store.FetchResult
		Error, Hint string
	}
	if err := c.do(ctx, "GET", "/api/events/next", q, "", nil, &out); err != nil {
		return store.FetchResult{}, err
	}
	if out.Error != "" {
		return store.FetchResult{}, &Error{Status: http.StatusNotFound, Code: out.Error, Hint: out.Hint}
	}
	return out.FetchResult, nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, contentType string, body []byte, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	send := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set(auth.Header, "1")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		return c.http().Do(req)
	}
	res, err := send()
	if isUnreachable(err) && c.Start != nil {
		if serr := c.Start(ctx); serr != nil {
			return serr
		}
		res, err = send()
	}
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct{ Error, Hint string }
		json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return &Error{Status: res.StatusCode, Code: e.Error, Hint: e.Hint}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("reading the answer of %s %s: %w", method, path, err)
	}
	return nil
}

// isUnreachable reports whether nothing listens at the address. Other
// errors, e.g. a reset in the middle of a request, are not retried.
func isUnreachable(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}
