package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"frp-tui/internal/model"
)

type Client struct{ http *http.Client }

func New(socket string) *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func (c *Client) request(ctx context.Context, method, path string, body, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost/v1"+path, input)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var failure model.APIError
		if err := json.NewDecoder(resp.Body).Decode(&failure); err != nil {
			return fmt.Errorf("守护进程返回 HTTP %d", resp.StatusCode)
		}
		return &Error{resp.StatusCode, failure.Error}
	}
	if result == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *Client) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	return c.request(ctx, "GET", "/health", nil, nil)
}

func (c *Client) State() (s model.Snapshot, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = c.request(ctx, "GET", "/state", nil, &s)
	return
}
func (c *Client) Create(in model.Connection) (out model.Connection, err error) {
	err = c.request(context.Background(), "POST", "/connections", in, &out)
	return
}
func (c *Client) Update(in model.Connection) (out model.Connection, err error) {
	err = c.request(context.Background(), "PUT", "/connections/"+in.ID, in, &out)
	return
}
func (c *Client) Delete(id string, revision int64) error {
	return c.request(context.Background(), "DELETE", "/connections/"+id, model.Action{Revision: revision}, nil)
}
func (c *Client) Action(id, action string, revision int64) (out model.Connection, err error) {
	err = c.request(context.Background(), "POST", "/connections/"+id+"/"+action, model.Action{Revision: revision}, &out)
	return
}
func (c *Client) Logs(id string, after uint64) (out model.Logs, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = c.request(ctx, "GET", "/connections/"+id+"/logs?after="+strconv.FormatUint(after, 10), nil, &out)
	return
}
func (c *Client) Settings(in model.Settings) (out model.Config, err error) {
	err = c.request(context.Background(), "PUT", "/settings", in, &out)
	return
}
func (c *Client) Shutdown() error {
	return c.request(context.Background(), "POST", "/shutdown", nil, nil)
}
