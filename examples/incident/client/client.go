// Package client is the typed HTTP client the CLI and TUI share. It speaks
// the server's patch protocol: reads return incidents, writes send
// deep.Patch[model.Incident] and return what became of each operation.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/server"
)

// Client talks to one incidentd.
type Client struct {
	base   string
	token  string
	author string
	http   *http.Client
}

// New builds a client. base is the server URL, token may be empty, author
// names this user in the audit log.
func New(base, token, author string) *Client {
	return &Client{
		base:   strings.TrimRight(base, "/"),
		token:  token,
		author: author,
		http:   &http.Client{},
	}
}

// WSURL returns the websocket address for an incident's notes room, token
// included — what deepws.Dial takes.
func (c *Client) WSURL(id string) string {
	ws := strings.Replace(c.base, "http", "ws", 1)
	q := url.Values{"room": {id}}
	if c.token != "" {
		q.Set("token", c.token)
	}
	return ws + "/ws?" + q.Encode()
}

// Author returns the name writes are attributed to.
func (c *Client) Author() string { return c.author }

// StatusError is a non-2xx reply, with the server's own message and, when
// the reply carried them, the per-operation outcomes.
type StatusError struct {
	Code    int
	Message string
	Result  server.Result
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("server returned %d: %s", e.Code, e.Message)
}

func (c *Client) do(method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Author", c.author)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		se := &StatusError{Code: resp.StatusCode, Message: strings.TrimSpace(string(data))}
		var detail struct {
			Error string `json:"error"`
			server.Result
		}
		if json.Unmarshal(data, &detail) == nil && detail.Error != "" {
			se.Message, se.Result = detail.Error, detail.Result
		}
		return se
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Create registers a new incident.
func (c *Client) Create(inc model.Incident) (model.Incident, error) {
	var created model.Incident
	err := c.do("POST", "/incidents", inc, &created)
	return created, err
}

// Get fetches one incident.
func (c *Client) Get(id string) (model.Incident, error) {
	var inc model.Incident
	err := c.do("GET", "/incidents/"+id, nil, &inc)
	return inc, err
}

// List fetches every incident.
func (c *Client) List() ([]model.Incident, error) {
	var incs []model.Incident
	err := c.do("GET", "/incidents", nil, &incs)
	return incs, err
}

// Patch submits a patch and reports each operation's fate.
func (c *Client) Patch(id string, p deep.Patch[model.Incident]) (server.Result, error) {
	var res server.Result
	err := c.do("POST", "/incidents/"+id+"/patch", p, &res)
	return res, err
}

// History fetches the audit log, oldest first.
func (c *Client) History(id string) ([]server.LogEntry, error) {
	var log []server.LogEntry
	err := c.do("GET", "/incidents/"+id+"/history", nil, &log)
	return log, err
}

// Undo asks the server to reverse one audit entry.
func (c *Client) Undo(id string, seq int64) (server.Result, error) {
	var res server.Result
	err := c.do("POST", "/incidents/"+id+"/undo", map[string]int64{"seq": seq}, &res)
	return res, err
}

// Compact collapses the audit log down to a baseline plus the last keep
// entries, and reports how many remain.
func (c *Client) Compact(id string, keep int) (int, error) {
	var out struct {
		Entries int `json:"entries"`
	}
	err := c.do("POST", "/incidents/"+id+"/compact", map[string]int{"keep": keep}, &out)
	return out.Entries, err
}
