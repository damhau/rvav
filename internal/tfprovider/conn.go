package tfprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// conn is the configured provider: a client of one rvav.
type conn struct {
	api    *rvavclient.ClientWithResponses
	server string
	// poll is how often a wait reads a request again.
	poll time.Duration
}

const jsonType = "application/json"

// send is v as a request body.
func send(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// object is the JSON object rvav answered, nil for 204; an error for a
// call rvav refused.
func object(resp *http.Response, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := rvavclient.Check(resp, b); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return nil, fmt.Errorf("rvav's answer isn't a JSON object: %w", err)
	}
	return out, nil
}

// done is a call that answers nothing worth reading.
func done(resp *http.Response, err error) error {
	_, err = object(resp, err)
	return err
}

// items are a list's items.
func items(obj map[string]any) []map[string]any {
	arr, _ := obj["items"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// find is the item of a list whose key is value, nil when none is.
func find(list map[string]any, err error, key, value string) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	for _, it := range items(list) {
		if s, _ := it[key].(string); s == value {
			return it, nil
		}
	}
	return nil, nil
}

func notFound(err error) bool { return rvavclient.IsStatus(err, http.StatusNotFound) }

// gone is a read that found nothing: nil, nil, so that Terraform forgets
// the resource.
func gone(obj map[string]any, err error) (map[string]any, error) {
	if notFound(err) {
		return nil, nil
	}
	return obj, err
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
