// Package mcptools exposes vitrine to agents over MCP. The tools call the
// HTTP API with the agent token, so agents never see it
// (docs/adr/0002-authentication.md).
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/obuchmann/vitrine/internal/browser"
	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/store"
	"github.com/obuchmann/vitrine/internal/web"
)

// Bounds for wait_for_events. Without a progress token the call returns
// before common client limits of 5 minutes for an idle call.
const (
	defaultWait   = 240 * time.Second
	maxWait       = 15 * time.Minute
	progressEvery = 20 * time.Second
)

// Options configure the tools.
type Options struct {
	Client  *client.Client
	Dir     string             // the agent's working directory; publish paths are relative to it
	Open    func(string) error // opens a URL in the browser; nil never opens one
	Version string             // of this binary, compared with the server's
}

// Register adds all tools to server.
func Register(server *mcp.Server, o Options) {
	t := &tools{Options: o, opened: map[string]bool{}}
	c := o.Client

	mcp.AddTool(server, &mcp.Tool{
		Name: "publish_prototype",
		Description: "Publish an HTML file as a prototype, or replace it with a new version. vitrine reads the file itself, " +
			"so the HTML does not pass through your context. Same name = next version: open tabs reload, the inbox stays. " +
			"The first publish opens the page in the user's browser.",
	}, t.publish)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "extend_prototype",
		Description: "Set the minimum lifetime (keep_for) of a prototype without publishing again, e.g. for a test over several days.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in extendIn) (*mcp.CallToolResult, web.PublishResult, error) {
		out, err := c.Extend(ctx, in.Name, in.KeepFor)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_prototypes",
		Description: "All prototypes with version, expiry, unread events and context (branch, task). " +
			"Start here in a new session to find where you left off.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listOut, error) {
		infos, err := c.List(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{Prototypes: make([]protoOut, 0, len(infos))}
		for _, info := range infos {
			out.Prototypes = append(out.Prototypes, protoOut{Info: info, URL: c.URL(info.Name)})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_prototype",
		Description: "One prototype: version, expiry, unread events, visible tabs (viewers), context. unknown_prototype means: publish it again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, protoOut, error) {
		info, err := c.Get(ctx, in.Name)
		if err != nil {
			return nil, protoOut{}, err
		}
		return nil, protoOut{Info: info, URL: c.URL(info.Name)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "wait_for_events",
		Description: "Return unacknowledged events of a prototype, or wait until one arrives. Pass the cursor of the " +
			"previous result as ack once you processed it; it acknowledges only the events you received, so events of types you " +
			"filtered out stay unread. ack=0 returns everything not yet acknowledged (use it in a new session). " +
			"Returns empty with timed_out after timeout_s (default 240): just call again. To keep working while waiting, " +
			"use the background curl from the server instructions instead.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, fetchOut, error) {
		wait := defaultWait
		if in.TimeoutS > 0 {
			wait = min(time.Duration(in.TimeoutS)*time.Second, maxWait)
		}
		if token := req.Params.GetProgressToken(); token != nil {
			stop := keepProgress(ctx, req, token)
			defer stop()
		}
		res, err := c.Next(ctx, in.Name, in.Types, in.Ack, wait)
		if err != nil {
			return nil, fetchOut{}, err
		}
		return nil, fetchOut{Events: toEvents(res.Events), Cursor: res.Cursor, UnreadLeft: res.UnreadLeft, TimedOut: res.TimedOut, Warning: res.Warning}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "Every stored event of a prototype, acknowledged or not, oldest first. Does not acknowledge anything.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listEventsIn) (*mcp.CallToolResult, eventsOut, error) {
		evs, err := c.Events(ctx, in.Name, in.Types, in.Since)
		if err != nil {
			return nil, eventsOut{}, err
		}
		return nil, eventsOut{Events: toEvents(evs)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "send_to_prototype",
		Description: "Send a message to every visible tab of a prototype (delivered_to counts them; hidden tabs miss it). " +
			"hub.js shows it as a toast unless the prototype " +
			"handles it with hub.on(\"message\", fn). New versions reload open tabs by themselves; no message needed for that.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, sendOut, error) {
		var data json.RawMessage
		if in.Data != nil {
			b, err := json.Marshal(in.Data)
			if err != nil {
				return nil, sendOut{}, err
			}
			data = b
		}
		n, err := c.Message(ctx, in.Name, in.Message, data)
		if err != nil {
			return nil, sendOut{}, err
		}
		return nil, sendOut{DeliveredTo: n}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_prototype",
		Description: "Remove a prototype and its events at once.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, deleteOut, error) {
		if err := c.Delete(ctx, in.Name); err != nil {
			return nil, deleteOut{}, err
		}
		return nil, deleteOut{Deleted: in.Name}, nil
	})
}

type tools struct {
	Options

	mu      sync.Mutex
	opened  map[string]bool // prototypes this session showed in the browser
	checked bool            // the server version was compared
}

func (t *tools) publish(ctx context.Context, _ *mcp.CallToolRequest, in publishIn) (*mcp.CallToolResult, publishOut, error) {
	if in.Path == "" {
		return nil, publishOut{}, errors.New("path is required: the HTML file to publish, relative to your working directory")
	}
	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.Dir, path)
	}
	name, err := resolveName(t.Dir, path, in.Name)
	if err != nil {
		return nil, publishOut{}, err
	}
	html, err := os.ReadFile(path)
	if err != nil {
		return nil, publishOut{}, err
	}
	res, err := t.Client.Publish(ctx, client.PublishInput{Name: name, HTML: html, KeepFor: in.KeepFor, Context: in.Context})
	if err != nil {
		return nil, publishOut{}, err
	}
	if note := t.checkVersion(ctx); note != "" {
		res.Warning = strings.TrimSpace(res.Warning + " " + note)
	}
	return nil, publishOut{PublishResult: res, Browser: t.show(res.URL, name)}, nil
}

// checkVersion warns once per session if the running server is another
// version, e.g. a daemon that kept running across an upgrade.
func (t *tools) checkVersion(ctx context.Context) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.checked || t.Version == "" || t.Version == "dev" {
		return ""
	}
	t.checked = true
	v, err := t.Client.Health(ctx)
	if err != nil || v == t.Version || v == "dev" {
		return ""
	}
	return fmt.Sprintf("vitrine %s is running, but vitrine mcp is %s. Tell the user to restart it: "+
		"\"vitrine service install\" for the service; otherwise it exits after 30 minutes without requests.", v, t.Version)
}

// show opens a prototype in the browser the first time this session
// publishes it. Later versions reload the open tab by themselves.
func (t *tools) show(url, name string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.opened[name] {
		return ""
	}
	if t.Open == nil {
		// Said once per prototype, so the agent does not tell the user the
		// page opened by itself.
		t.opened[name] = true
		return "not opened (--no-open). Give the user the URL."
	}
	if err := t.Open(url); err != nil {
		if errors.Is(err, browser.ErrNoDesktop) {
			return "not opened: no desktop session. Give the user the URL."
		}
		return fmt.Sprintf("not opened (%v). Give the user the URL.", err)
	}
	t.opened[name] = true
	return "opened"
}

// keepProgress sends a progress notification every 20 s while a call waits,
// so clients with an idle limit keep the call open.
func keepProgress(ctx context.Context, req *mcp.CallToolRequest, token any) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(progressEvery)
		defer t.Stop()
		n := 0.0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n++
				req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: token, Progress: n, Message: "waiting for events"})
			}
		}
	}()
	return cancel
}

type publishIn struct {
	Path    string            `json:"path" jsonschema:"the HTML file, relative to your working directory; one self-contained document with CSS, JS and images inline"`
	Name    string            `json:"name,omitempty" jsonschema:"<repo>/<name>, or just <name>; repo defaults to the git repository that contains the file, name to the file's folder (or file name without .html)"`
	KeepFor string            `json:"keep_for,omitempty" jsonschema:"minimum lifetime, e.g. 10d or 36h, at most 30d"`
	Context map[string]string `json:"context,omitempty" jsonschema:"where this comes from, e.g. branch, task; shown to later sessions"`
}

type publishOut struct {
	web.PublishResult
	Browser string `json:"browser,omitempty" jsonschema:"whether the page was opened in the user's browser"`
}

type extendIn struct {
	Name    string `json:"name" jsonschema:"<repo>/<name>"`
	KeepFor string `json:"keep_for" jsonschema:"minimum lifetime, e.g. 10d or 36h, at most 30d"`
}

type nameIn struct {
	Name string `json:"name" jsonschema:"<repo>/<name>"`
}

type waitIn struct {
	Name     string   `json:"name" jsonschema:"<repo>/<name>"`
	Types    []string `json:"types,omitempty" jsonschema:"only these event types, e.g. choice, comment; empty = all"`
	Ack      int64    `json:"ack,omitempty" jsonschema:"cursor of the previous result once you processed it; 0 acknowledges nothing"`
	TimeoutS int      `json:"timeout_s,omitempty" jsonschema:"seconds to wait, default 240, at most 900"`
}

type listEventsIn struct {
	Name  string   `json:"name" jsonschema:"<repo>/<name>"`
	Types []string `json:"types,omitempty" jsonschema:"only these event types; empty = all"`
	Since int64    `json:"since,omitempty" jsonschema:"only events with a seq greater than this"`
}

type sendIn struct {
	Name    string `json:"name" jsonschema:"<repo>/<name>"`
	Message string `json:"message" jsonschema:"short text shown in the prototype"`
	Data    any    `json:"data,omitempty" jsonschema:"optional data for hub.on(\"message\") handlers"`
}

type protoOut struct {
	store.Info
	URL string `json:"url"`
}

type listOut struct {
	Prototypes []protoOut `json:"prototypes"`
}

// event mirrors store.Event with data as a plain value: the schema the SDK
// derives from json.RawMessage would only allow arrays.
type event struct {
	Seq     int64     `json:"seq"`
	ID      string    `json:"id"`
	Type    string    `json:"type"`
	Data    any       `json:"data,omitempty"`
	Version int       `json:"version"`
	Auto    bool      `json:"auto,omitempty"`
	At      time.Time `json:"at"`
}

func toEvents(evs []store.Event) []event {
	out := make([]event, 0, len(evs))
	for _, ev := range evs {
		e := event{Seq: ev.Seq, ID: ev.ID, Type: ev.Type, Version: ev.Version, Auto: ev.Auto, At: ev.At}
		if len(ev.Data) > 0 {
			json.Unmarshal(ev.Data, &e.Data)
		}
		out = append(out, e)
	}
	return out
}

type fetchOut struct {
	Events     []event `json:"events"`
	Cursor     int64   `json:"cursor" jsonschema:"pass as ack once you processed these events"`
	UnreadLeft int     `json:"unread_left" jsonschema:"unread events not in this result, including other types"`
	TimedOut   bool    `json:"timed_out,omitempty" jsonschema:"nothing arrived in time: call again"`
	Warning    string  `json:"warning,omitempty" jsonschema:"pass it on to the user"`
}

type eventsOut struct {
	Events []event `json:"events"`
}

type sendOut struct {
	DeliveredTo int `json:"delivered_to"`
}

type deleteOut struct {
	Deleted string `json:"deleted"`
}
