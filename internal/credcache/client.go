package credcache

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ErrUnsupported reports a platform without verified per-user IPC.
var ErrUnsupported = errors.New("the credential cache is not available on this platform")

// Client talks to the broker of one home.
type Client struct {
	Home string
	// Spawn starts a detached broker; nil means the caller cannot start one.
	Spawn func(home string) error
}

// Grant is a key plus how it was obtained.
type Grant struct {
	Token      string
	Generation uint64
	// Cached reports the broker holds this key; Fetched that this call ran
	// the helper. Fetched without Cached means a lock, or a broker loss, kept
	// the key out of the cache, and it serves this command only.
	Cached    bool
	Fetched   bool
	IdleUntil time.Time
	HardUntil time.Time
}

// Request is one key lookup. Resolve runs only when this client won the fetch.
type Request struct {
	Key      Key
	NotAfter time.Time
	Resolve  func() (string, error)
}

// UnavailableError means the broker could not be reached after one restart.
type UnavailableError struct{ Err error }

func (e *UnavailableError) Error() string { return "credential cache unavailable: " + e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// CooldownError means a recent fetch of this key was refused or failed.
type CooldownError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *CooldownError) Error() string {
	seconds := int(e.RetryAfter.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	message := e.Message
	if message == "" {
		message = "the last credential request failed"
	}
	return fmt.Sprintf("not asking 1Password again for %ds: %s", seconds, message)
}

type connection struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *connection) call(req request) (response, error) {
	req.V = protocolVersion
	data, err := json.Marshal(req)
	if err != nil {
		return response{}, err
	}
	timeout := callTimeout
	if req.Op == "acquire" {
		timeout = acquireTimeout
	}
	if err = c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return response{}, err
	}
	if _, err = c.conn.Write(append(data, '\n')); err != nil {
		return response{}, err
	}
	line, err := readFrame(c.reader)
	if err != nil {
		return response{}, err
	}
	var resp response
	if err = json.Unmarshal(line, &resp); err != nil {
		return response{}, err
	}
	return resp, nil
}

func (c *Client) socketPath() (string, error) {
	dir, err := runtimeDir(c.Home)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, socketName), nil
}

// dial connects to a running broker and never starts one. The directory is
// checked first: its permissions are what confine the socket to this user.
func (c *Client) dial() (*connection, error) {
	path, err := c.socketPath()
	if err != nil {
		return nil, err
	}
	if err = verifyPrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	// Check the listener too: a key must never be handed to an impostor.
	if !peerIsCurrentUser(conn) {
		conn.Close()
		return nil, errors.New("the broker socket belongs to another user")
	}
	return &connection{conn: conn, reader: bufio.NewReader(conn)}, nil
}

// connect dials the broker, starting it once if it is not running. Concurrent
// starts are safe: the broker lock lets only one of them serve.
func (c *Client) connect() (*connection, error) {
	if conn, err := c.dial(); err == nil {
		return conn, nil
	}
	if c.Spawn == nil {
		return nil, errors.New("no broker is running")
	}
	path, err := c.socketPath()
	if err != nil {
		return nil, err
	}
	if err = ensurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err = c.Spawn(c.Home); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := c.dial()
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the broker did not start: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Get returns the key from the broker or fetches it once. A lost broker gets
// exactly one restart; after that the caller reports an actionable error
// rather than falling back to asking on every command.
func (c *Client) Get(req Request, settings Settings) (Grant, error) {
	if !Supported {
		return Grant{}, ErrUnsupported
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		conn, err := c.connect()
		if err != nil {
			lastErr = err
			continue
		}
		grant, retry, err := c.getOnce(conn, req, settings)
		conn.conn.Close()
		if !retry {
			return grant, err
		}
		lastErr = err
	}
	return Grant{}, &UnavailableError{Err: lastErr}
}

func (c *Client) getOnce(conn *connection, req Request, settings Settings) (Grant, bool, error) {
	id := req.Key.ID()
	// A short ping first: a stalled broker fails now, not after a long acquire.
	resp, err := conn.call(request{Op: "ping"})
	if err == nil && resp.State == stateProtocol {
		return Grant{}, true, errors.New("a different my release was running the broker")
	}
	if err == nil {
		resp, err = conn.call(request{Op: "acquire", Key: id, NotAfter: req.NotAfter})
	}
	if err != nil {
		return Grant{}, true, err
	}
	switch resp.State {
	case stateHit:
		return Grant{Token: resp.Token, Generation: resp.Generation, Cached: true, IdleUntil: resp.IdleUntil, HardUntil: resp.HardUntil}, false, nil
	case stateResolve:
		return c.fetch(conn, req, settings, resp.Lease)
	case stateCooldown:
		return Grant{}, false, &CooldownError{RetryAfter: resp.RetryAfter, Message: resp.Message}
	case stateLocked:
		return Grant{}, false, errors.New("the credential cache was locked while this command waited; rerun it")
	case stateTimeout:
		return Grant{}, false, errors.New("timed out waiting for another my command's credential request; rerun it")
	case stateProtocol:
		return Grant{}, true, errors.New("a different my release was running the broker")
	default:
		return Grant{}, true, fmt.Errorf("unexpected broker reply %q", resp.State)
	}
}

// fetch runs the helper as the lease owner. It publishes on the connection
// that took the lease and never on a new one, so neither a lock nor a broker
// restart can be bypassed by a fetch already under way.
func (c *Client) fetch(conn *connection, req Request, settings Settings, lease string) (Grant, bool, error) {
	id := req.Key.ID()
	token, err := req.Resolve()
	if err != nil {
		_, _ = conn.call(request{Op: "abandon", Key: id, Lease: lease, Message: err.Error()})
		return Grant{}, false, err
	}
	// `cache off` may have landed during the prompt. Closing the connection
	// releases the lease with no cooldown.
	if current, loadErr := LoadSettings(c.Home); loadErr != nil || current.Disabled {
		return Grant{Token: token, Fetched: true}, false, nil
	}
	idle, hard := settings.Limits()
	resp, err := conn.call(request{Op: "publish", Key: id, Lease: lease, Token: token, Service: req.Key.Service,
		ManifestName: req.Key.ManifestName, Idle: idle, Max: hard, NotAfter: req.NotAfter})
	if err != nil || resp.State != stateOK {
		return Grant{Token: token, Fetched: true}, false, nil
	}
	return Grant{Token: token, Generation: resp.Generation, Cached: true, Fetched: true, IdleUntil: resp.IdleUntil, HardUntil: resp.HardUntil}, false, nil
}

// Invalidate drops the key if it is still the given generation; generation 0
// drops whichever key is there. No running broker means nothing to drop.
func (c *Client) Invalidate(key Key, generation uint64) error {
	return c.withRunning(request{Op: "invalidate", Key: key.ID(), Generation: generation}, nil)
}

// Lock clears every key and fences fetches under way. It never starts a broker.
func (c *Client) Lock() error {
	return c.withRunning(request{Op: "lock"}, nil)
}

// Status lists cached keys without their values. It never starts a broker.
func (c *Client) Status() ([]EntryStatus, error) {
	var out []EntryStatus
	err := c.withRunning(request{Op: "status"}, func(resp response) { out = resp.Entries })
	return out, err
}

func (c *Client) withRunning(req request, use func(response)) error {
	if !Supported {
		return nil
	}
	conn, err := c.dial()
	if err != nil {
		// No listener, or no private directory yet, means no broker holds keys.
		if errors.Is(err, os.ErrNotExist) || isNotRunning(err) {
			return nil
		}
		return err
	}
	defer conn.conn.Close()
	resp, err := conn.call(req)
	if err != nil {
		return err
	}
	if resp.State == stateProtocol {
		return nil
	}
	if use != nil {
		use(resp)
	}
	return nil
}
