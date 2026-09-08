package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/brunoga/deep/examples/fieldwork/server"
)

// Client is the device's link to the office — used only when there is a
// signal. Everything else the technician does works against [Local].
type Client struct {
	base   string
	token  string
	author string
	http   *http.Client
}

// New builds a client.
func New(base, token, author string) *Client {
	return &Client{
		base:   strings.TrimRight(base, "/"),
		token:  token,
		author: author,
		http:   &http.Client{},
	}
}

// StatusError is a non-2xx reply.
type StatusError struct {
	Code    int
	Message string
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
		}
		if json.Unmarshal(data, &detail) == nil && detail.Error != "" {
			se.Message = detail.Error
		}
		return se
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Author returns the name this device's changes are attributed to.
func (c *Client) Author() string { return c.author }

// SyncReport is what one round trip did, in the technician's terms.
type SyncReport struct {
	Pushed    int
	Merged    int
	Rejected  []server.PushResult
	Conflicts []server.PushResult
	Updated   int
}

// Sync performs the whole round trip: everything this device owes goes up as
// diffs, and whatever the office changed comes back down. The authoritative
// state in the reply is adopted as both shadow and working copy — including
// where the merge policy overrode a local edit, which is what the returned
// conflicts describe.
//
// A rejected asset is the exception: its local work is deliberately kept, so
// the technician can fix what the server refused and try again.
func (c *Client) Sync(local *Local) (SyncReport, error) {
	pending, err := local.Pending()
	if err != nil {
		return SyncReport{}, err
	}
	req := server.SyncRequest{Known: local.Known()}
	for _, p := range pending {
		req.Pushes = append(req.Pushes, server.Push{
			ID: p.ID, BaseVersion: p.BaseVersion, Patch: p.Patch,
		})
	}

	var resp server.SyncResponse
	if err := c.do("POST", "/sync", req, &resp); err != nil {
		return SyncReport{}, err
	}

	var report SyncReport
	for _, res := range resp.Results {
		switch res.Outcome {
		case "rejected":
			// Nothing moves: the shadow still describes a state the server
			// really held, and the working copy still holds the technician's
			// work. The next sync recomputes the diff — so fixing whatever
			// the server refused is all it takes to retry.
			report.Rejected = append(report.Rejected, res)
			continue
		case "merged":
			report.Merged++
		default:
			report.Pushed++
		}
		if len(res.Conflicts) > 0 {
			report.Conflicts = append(report.Conflicts, res)
		}
		if err := local.Adopt(res.Asset, res.Version); err != nil {
			return report, err
		}
	}
	for _, upd := range resp.Updated {
		if err := local.Adopt(upd.Asset, upd.Version); err != nil {
			return report, err
		}
		report.Updated++
	}
	return report, nil
}

// Assets fetches the full inventory — how a fresh device gets started.
func (c *Client) Assets() (server.ListResponse, error) {
	var out server.ListResponse
	err := c.do("GET", "/assets", nil, &out)
	return out, err
}

// Get fetches one asset with its version.
func (c *Client) Get(id string) (server.GetResponse, error) {
	var out server.GetResponse
	err := c.do("GET", "/assets/"+id, nil, &out)
	return out, err
}

// History fetches an asset's version log.
func (c *Client) History(id string) ([]server.VersionEntry, error) {
	var out []server.VersionEntry
	err := c.do("GET", "/assets/"+id+"/history", nil, &out)
	return out, err
}
