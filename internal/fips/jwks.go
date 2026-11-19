package fips

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// maxJWKS bounds the key sets read; go-oidc reads no more either.
const maxJWKS = 1 << 20

// JWKSClient is client, for fetching JSON Web Key Sets: in FIPS 140 mode
// it drops each key's certificate chain (x5c) and thumbprints. go-jose
// checks a chain's SHA-1 thumbprint as it reads a key, and SHA-1 panics in
// FIPS 140-only mode, in go-oidc's goroutine that fetches keys; rvav
// checks signatures with the keys alone. Keycloak, Entra ID and the Bot
// Framework all publish x5c.
func JWKSClient(client *http.Client) *http.Client {
	if Current() == Off {
		return client
	}
	c := *client
	c.Transport = withoutX5C{next: orDefault(client.Transport)}
	return &c
}

func orDefault(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		return http.DefaultTransport
	}
	return rt
}

type withoutX5C struct{ next http.RoundTripper }

func (t withoutX5C) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKS))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	body = StripX5C(body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return resp, nil
}

// StripX5C is a JSON Web Key Set without its keys' certificate chains and
// thumbprints; anything else is returned as it is.
func StripX5C(body []byte) []byte {
	var set map[string]json.RawMessage
	if json.Unmarshal(body, &set) != nil || set["keys"] == nil {
		return body
	}
	var keys []map[string]json.RawMessage
	if json.Unmarshal(set["keys"], &keys) != nil {
		return body
	}
	for _, k := range keys {
		for _, field := range []string{"x5c", "x5t", "x5t#S256", "x5u"} {
			delete(k, field)
		}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return body
	}
	set["keys"] = raw
	out, err := json.Marshal(set)
	if err != nil {
		return body
	}
	return out
}
