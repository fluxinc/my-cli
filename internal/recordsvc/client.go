package recordsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// Record is one record as the service returns it. Its shape beyond id,
// revision fields and archived belongs to the service.
type Record map[string]any

// ID is the record's stable id.
func (r Record) ID() string { return str(r["id"]) }

// Revisions returns every revision field the record carries ("revision" and
// any "*_revision"), which a write must echo back.
func (r Record) Revisions() map[string]any {
	out := map[string]any{}
	for key, value := range r {
		if value != nil && (key == "revision" || strings.HasSuffix(key, "_revision")) {
			out[key] = value
		}
	}
	return out
}

// Client talks to one service.
type Client struct {
	ServiceID string
	base      *url.URL
	token     string
	http      *http.Client
}

// NewClient validates the base URL and builds a client that never follows a
// redirect to another origin, so the credential cannot leave the service.
func NewClient(serviceID, baseURL, token string) (*Client, error) {
	if err := manifest.ValidateAPIBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("service %s connection.url: %w", serviceID, err)
	}
	base, _ := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
				return fmt.Errorf("refused a redirect to another origin (%s)", req.URL.Host)
			}
			return nil
		},
	}
	return &Client{ServiceID: serviceID, base: base, token: token, http: client}, nil
}

// Error is a refusal or failure the service reported, or one reaching it.
type Error struct {
	ServiceID string
	Status    int    // 0 when the service could not be reached
	Code      string // the service's error code: stale, invalid, not_editable, refused, not_found...
	Message   string
	Field     string
	Messages  []string
	Record    Record // the current record, when the service sent it (a stale write)
	// For confirmation_required: the other records the change would touch,
	// and where people can review them.
	Affected  []string
	ReviewURL string
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("service %s is unavailable: %s", e.ServiceID, e.Message)
	}
	parts := []string{fmt.Sprintf("service %s refused (%d %s)", e.ServiceID, e.Status, e.Code)}
	if e.Field != "" {
		parts = append(parts, "field "+e.Field)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	parts = append(parts, e.Messages...)
	return strings.Join(parts, ": ")
}

// Stale reports a revision conflict: someone changed the record first.
func (e *Error) Stale() bool { return e.Status == http.StatusConflict && e.Code == "stale" }

// Unavailable reports that the service could not be reached or failed.
func (e *Error) Unavailable() bool { return e.Status == 0 || e.Status >= 500 }

// NotFound reports a 404.
func (e *Error) NotFound() bool { return e.Status == http.StatusNotFound }

// Get reads one record by its stable id.
func (c *Client) Get(ctx context.Context, id string) (Record, error) {
	var rec Record
	err := c.do(ctx, http.MethodGet, "records/"+url.PathEscape(id), nil, nil, &rec)
	return rec, err
}

// Resolve returns every record an exact identifier names.
func (c *Client) Resolve(ctx context.Context, identifier string) ([]Record, error) {
	var out struct {
		Candidates []Record `json:"candidates"`
	}
	err := c.do(ctx, http.MethodGet, "records/resolve", url.Values{"identifier": {identifier}}, nil, &out)
	return out.Candidates, err
}

// maxSearchPages bounds a search: a registry search that spans more pages
// than this is a query to narrow, not a page loop to run.
const maxSearchPages = 20

// Search returns the records matching q (all records when q is empty), in
// the service's order. It reads every page, up to maxSearchPages.
func (c *Client) Search(ctx context.Context, q string, includeArchived bool) ([]Record, error) {
	var all []Record
	for page := 1; ; page++ {
		params := url.Values{"page": {fmt.Sprint(page)}}
		if q != "" {
			params.Set("q", q)
		}
		if includeArchived {
			params.Set("include_archived", "true")
		}
		var out struct {
			Records  []Record `json:"records"`
			NextPage any      `json:"next_page"`
		}
		if err := c.do(ctx, http.MethodGet, "records", params, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Records...)
		if out.NextPage == nil {
			return all, nil
		}
		if page >= maxSearchPages {
			return nil, &Error{ServiceID: c.ServiceID, Status: http.StatusRequestEntityTooLarge, Code: "too_many",
				Message: fmt.Sprintf("more than %d pages match; narrow the search", maxSearchPages)}
		}
	}
}

// Patch writes several fields at once. The service saves all of them or
// none, against the revisions given. confirmed names the other records a
// shared change also touches, exactly as a confirmation_required refusal
// listed them; nil when nothing was confirmed.
func (c *Client) Patch(ctx context.Context, id string, changes map[string]string, revisions map[string]any, confirmed []string) (Record, error) {
	body := map[string]any{"changes": changes}
	if len(confirmed) != 0 {
		body["confirmed_ids"] = confirmed
	}
	for key, value := range revisions {
		body[key] = value
	}
	var rec Record
	err := c.do(ctx, http.MethodPatch, "records/"+url.PathEscape(id), nil, body, &rec)
	return rec, err
}

// Transition runs one workflow operation the service gates.
func (c *Client) Transition(ctx context.Context, id, operation string, params map[string]string, revisions map[string]any) (Record, error) {
	body := map[string]any{"operation": operation}
	for key, value := range params {
		body[key] = value
	}
	for key, value := range revisions {
		body[key] = value
	}
	var rec Record
	err := c.do(ctx, http.MethodPost, "records/"+url.PathEscape(id)+"/transitions", nil, body, &rec)
	return rec, err
}

// Reference names a record kept outside the service, such as a support
// record: exact and namespaced, so two organizations' ids never collide.
type Reference struct {
	Namespace string `json:"namespace"`
	System    string `json:"system"`
	ID        string `json:"id"`
}

// Link adds a note to a record naming a reference kept elsewhere. key makes
// the request idempotent: the service answers a retry (any key, same
// reference and body) with the note it already made. created is false for
// such a replay.
func (c *Client) Link(ctx context.Context, id, key string, ref Reference, body string, revisions map[string]any) (note map[string]any, created bool, err error) {
	payload := map[string]any{"reference": ref, "body": body}
	for k, v := range revisions {
		payload[k] = v
	}
	var out struct {
		Note map[string]any `json:"note"`
	}
	status, err := c.send(ctx, http.MethodPost, "records/"+url.PathEscape(id)+"/notes", nil, payload, &out, map[string]string{"Idempotency-Key": key})
	return out.Note, status == http.StatusCreated, err
}

// do sends one request. path is relative to the base and already escaped
// (ids go through url.PathEscape once, here and nowhere else).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	_, err := c.send(ctx, method, path, query, body, out, nil)
	return err
}

// send is do with extra request headers, reporting the success status.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, body any, out any, headers map[string]string) (int, error) {
	target, err := url.Parse(c.base.String() + path)
	if err != nil {
		return 0, err
	}
	if query != nil {
		target.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, &Error{ServiceID: c.ServiceID, Message: c.scrub(unwrapURLError(err))}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return 0, &Error{ServiceID: c.ServiceID, Message: "reading the response failed"}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(out); err != nil {
			return 0, &Error{ServiceID: c.ServiceID, Status: resp.StatusCode, Code: "bad_response", Message: "the response was not the expected JSON"}
		}
		return resp.StatusCode, nil
	}
	return 0, c.apiError(resp.StatusCode, data)
}

// apiError keeps only the service's structured fields, never the raw body,
// and strips the credential from anything it echoes.
func (c *Client) apiError(status int, data []byte) error {
	var parsed struct {
		Error     string   `json:"error"`
		Message   string   `json:"message"`
		Field     string   `json:"field"`
		Messages  []string `json:"messages"`
		Record    Record   `json:"record"`
		Affected  []string `json:"affected"`
		ReviewURL string   `json:"review_url"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&parsed)
	e := &Error{ServiceID: c.ServiceID, Status: status, Code: c.scrub(parsed.Error), Field: c.scrub(parsed.Field),
		Message: c.scrub(parsed.Message), ReviewURL: c.scrub(parsed.ReviewURL)}
	if parsed.Record != nil {
		scrubbed, _ := c.scrubValue(map[string]any(parsed.Record)).(map[string]any)
		e.Record = Record(scrubbed)
	}
	for _, m := range parsed.Messages {
		e.Messages = append(e.Messages, c.scrub(m))
	}
	for _, id := range parsed.Affected {
		e.Affected = append(e.Affected, c.scrub(id))
	}
	if e.Code == "" {
		e.Code = strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_"))
	}
	if status == http.StatusUnauthorized {
		e.Message = "the credential was not accepted; check the service's auth_ref"
	}
	return e
}

// scrubValue strips the credential from every string in a decoded JSON
// value, map keys included.
func (c *Client) scrubValue(value any) any {
	switch v := value.(type) {
	case string:
		return c.scrub(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[c.scrub(key)] = c.scrubValue(item)
		}
		return out
	case Record:
		return c.scrubValue(map[string]any(v))
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = c.scrubValue(item)
		}
		return out
	default:
		return v
	}
}

func (c *Client) scrub(text string) string {
	if c.token == "" {
		return text
	}
	return strings.ReplaceAll(text, c.token, "[redacted]")
}

func unwrapURLError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

func str(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ListAfter reads one keyset page of every record in stable-id order, for
// bootstrapping an index: after is "" to start, then the previous page's
// next; next is "" on the last page.
func (c *Client) ListAfter(ctx context.Context, after string, includeArchived bool) ([]Record, string, error) {
	params := url.Values{"after": {after}}
	if includeArchived {
		params.Set("include_archived", "true")
	}
	var out struct {
		Records   []Record `json:"records"`
		NextAfter *string  `json:"next_after"`
	}
	if err := c.do(ctx, http.MethodGet, "records", params, nil, &out); err != nil {
		return nil, "", err
	}
	next := ""
	if out.NextAfter != nil {
		next = *out.NextAfter
	}
	return out.Records, next, nil
}

// Change is one entry of the service's commit-safe change feed.
type Change struct {
	ID string `json:"id"`
	Op string `json:"op"` // upsert or tombstone
}

// Changes reads one page of the change feed after cursor ("" from the
// start). It returns the cursor to resume from and whether more follow.
func (c *Client) Changes(ctx context.Context, cursor string) ([]Change, string, bool, error) {
	params := url.Values{}
	if cursor != "" {
		params.Set("after", cursor)
	}
	var out struct {
		Changes []Change `json:"changes"`
		Cursor  *string  `json:"cursor"`
		More    bool     `json:"more"`
	}
	if err := c.do(ctx, http.MethodGet, "changes", params, nil, &out); err != nil {
		return nil, "", false, err
	}
	next := cursor
	if out.Cursor != nil && *out.Cursor != "" {
		next = *out.Cursor
	}
	return out.Changes, next, out.More, nil
}
