package credcache

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type entry struct {
	token        string
	generation   uint64
	service      string
	manifestName string
	idle         time.Duration
	idleUntil    time.Time
	hardUntil    time.Time
}

// pending is one in-flight fetch. Its owner holds a connection open for the
// whole fetch, so an owner that dies releases it immediately.
type pending struct {
	lease    string
	epoch    uint64
	deadline time.Time
	done     chan struct{}
}

type cooldown struct {
	until   time.Time
	message string
}

// Server is the broker's state; Serve runs it on a listener.
type Server struct {
	// Now, WaitTimeout, LeaseTimeout, ExitAfter and PeerAllowed have
	// production defaults; tests replace them.
	Now          func() time.Time
	WaitTimeout  time.Duration
	LeaseTimeout time.Duration
	ExitAfter    time.Duration
	PeerAllowed  func(net.Conn) bool

	mu         sync.Mutex
	entries    map[string]*entry
	pendings   map[string]*pending
	cooldowns  map[string]cooldown
	epoch      uint64
	generation uint64
	active     map[net.Conn]struct{}
	lastActive time.Time
	waiting    int
	stop       context.CancelFunc
}

// NewServer returns a broker with production timing.
func NewServer() *Server {
	return &Server{
		Now:          time.Now,
		WaitTimeout:  ResolveTimeout + 15*time.Second,
		LeaseTimeout: ResolveTimeout + 15*time.Second,
		ExitAfter:    10 * time.Minute,
		PeerAllowed:  peerIsCurrentUser,
		entries:      map[string]*entry{},
		pendings:     map[string]*pending{},
		cooldowns:    map[string]cooldown{},
		active:       map[net.Conn]struct{}{},
		generation:   randomGeneration(),
	}
}

// Serve accepts connections until the context ends, the broker is told to
// stop, or it has held nothing and served no one for ExitAfter.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.stop = cancel
	s.lastActive = s.Now()
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		listener.Close()
		// Closing connections also releases every lease they own.
		s.mu.Lock()
		defer s.mu.Unlock()
		for conn := range s.active {
			conn.Close()
		}
	}()
	go s.sweepLoop(ctx, cancel)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !s.PeerAllowed(conn) {
			conn.Close()
			continue
		}
		s.mu.Lock()
		s.active[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(ctx, conn)
	}
}

func (s *Server) sweepLoop(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.sweep() {
				cancel()
				return
			}
		}
	}
}

// sweep drops expired state and reports whether the broker should exit.
func (s *Server) sweep() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	s.expireLocked(now)
	if len(s.entries) != 0 || len(s.pendings) != 0 || len(s.active) != 0 {
		s.lastActive = now
		return false
	}
	return now.Sub(s.lastActive) >= s.ExitAfter
}

func (s *Server) expireLocked(now time.Time) {
	for key, e := range s.entries {
		if !now.Before(e.idleUntil) || !now.Before(e.hardUntil) {
			delete(s.entries, key)
		}
	}
	for key, p := range s.pendings {
		if !now.Before(p.deadline) {
			delete(s.pendings, key)
			s.cooldowns[key] = cooldown{until: now.Add(DenyCooldown), message: "an earlier credential request timed out"}
			close(p.done)
		}
	}
	for key, c := range s.cooldowns {
		if !now.Before(c.until) {
			delete(s.cooldowns, key)
		}
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	owned := map[string]string{} // key -> lease this connection owns
	defer func() {
		conn.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.active, conn)
		s.lastActive = s.Now()
		for key, lease := range owned {
			// The owner died or gave up without a verdict, so a waiter may
			// try: this is not a refusal and sets no cooldown.
			if p := s.pendings[key]; p != nil && p.lease == lease {
				delete(s.pendings, key)
				close(p.done)
			}
		}
	}()
	reader := bufio.NewReader(conn)
	for {
		line, err := readFrame(reader)
		if err != nil {
			return
		}
		var req request
		if err = json.Unmarshal(line, &req); err != nil {
			return
		}
		if req.V != protocolVersion {
			// A different my release owns this broker's socket: step aside so
			// the caller's own broker can start. Its keys are simply refetched.
			writeResponse(conn, response{State: stateProtocol})
			s.mu.Lock()
			stop := s.stop
			s.mu.Unlock()
			if stop != nil {
				stop()
			}
			return
		}
		if writeResponse(conn, s.apply(ctx, req, owned)) != nil {
			return
		}
	}
}

func writeResponse(w io.Writer, resp response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func (s *Server) apply(ctx context.Context, req request, owned map[string]string) response {
	switch req.Op {
	case "ping":
		return response{State: stateOK}
	case "acquire":
		return s.acquire(ctx, req, owned)
	case "publish":
		return s.publish(req, owned)
	case "abandon":
		return s.abandon(req, owned)
	case "invalidate":
		s.mu.Lock()
		defer s.mu.Unlock()
		// A stale 401 must not evict a newer key another command just fetched.
		if e := s.entries[req.Key]; e != nil && (req.Generation == 0 || e.generation == req.Generation) {
			delete(s.entries, req.Key)
		}
		// An unconditional drop (refresh, a replaced reference) also fences a
		// fetch under way, which could otherwise publish the old key next.
		if p := s.pendings[req.Key]; p != nil && req.Generation == 0 {
			delete(s.pendings, req.Key)
			close(p.done)
		}
		return response{State: stateOK}
	case "lock":
		return s.lock()
	case "status":
		return s.status()
	default:
		return response{State: stateRejected, Message: "unknown operation"}
	}
}

func (s *Server) acquire(ctx context.Context, req request, owned map[string]string) response {
	s.mu.Lock()
	startEpoch := s.epoch
	s.mu.Unlock()
	timeout := time.NewTimer(s.WaitTimeout)
	defer timeout.Stop()
	for {
		s.mu.Lock()
		now := s.Now()
		s.expireLocked(now)
		if s.epoch != startEpoch {
			s.mu.Unlock()
			return response{State: stateLocked}
		}
		if e := s.entries[req.Key]; e != nil && !req.NotAfter.IsZero() && !now.Before(req.NotAfter) {
			delete(s.entries, req.Key)
		}
		if e := s.entries[req.Key]; e != nil {
			// A later command may know a tighter end than the publisher did.
			if !req.NotAfter.IsZero() {
				e.hardUntil = earlier(e.hardUntil, req.NotAfter)
			}
			e.idleUntil = earlier(now.Add(e.idle), e.hardUntil)
			resp := response{State: stateHit, Token: e.token, Generation: e.generation, IdleUntil: e.idleUntil, HardUntil: e.hardUntil}
			s.mu.Unlock()
			return resp
		}
		if c, ok := s.cooldowns[req.Key]; ok {
			s.mu.Unlock()
			return response{State: stateCooldown, RetryAfter: c.until.Sub(now), Message: c.message}
		}
		p := s.pendings[req.Key]
		if p == nil {
			p = &pending{lease: newLease(), epoch: s.epoch, deadline: now.Add(s.LeaseTimeout), done: make(chan struct{})}
			s.pendings[req.Key] = p
			owned[req.Key] = p.lease
			s.mu.Unlock()
			return response{State: stateResolve, Lease: p.lease}
		}
		done := p.done
		s.waiting++
		s.mu.Unlock()
		var outcome *response
		select {
		case <-done:
		case <-timeout.C:
			// Never a cue to run the helper: that is how prompts fan out.
			outcome = &response{State: stateTimeout}
		case <-ctx.Done():
			outcome = &response{State: stateLocked}
		}
		s.mu.Lock()
		s.waiting--
		s.mu.Unlock()
		if outcome != nil {
			return *outcome
		}
	}
}

func (s *Server) publish(req request, owned map[string]string) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	s.expireLocked(now)
	p := s.pendings[req.Key]
	// The epoch fence: a fetch begun before a lock cannot refill the cache.
	if p == nil || p.lease != req.Lease || owned[req.Key] != req.Lease || p.epoch != s.epoch || req.Token == "" {
		return response{State: stateRejected}
	}
	delete(s.pendings, req.Key)
	delete(owned, req.Key)
	close(p.done)
	idle, hard := clampLimits(req.Idle, req.Max)
	hardUntil := now.Add(hard)
	if !req.NotAfter.IsZero() {
		hardUntil = earlier(hardUntil, req.NotAfter)
	}
	if !now.Before(hardUntil) {
		return response{State: stateRejected}
	}
	s.generation++
	e := &entry{token: req.Token, generation: s.generation, service: req.Service, manifestName: req.ManifestName,
		idle: idle, idleUntil: earlier(now.Add(idle), hardUntil), hardUntil: hardUntil}
	s.entries[req.Key] = e
	return response{State: stateOK, Generation: e.generation, IdleUntil: e.idleUntil, HardUntil: e.hardUntil}
}

func (s *Server) abandon(req request, owned map[string]string) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if p := s.pendings[req.Key]; p != nil && p.lease == req.Lease && owned[req.Key] == req.Lease {
		delete(s.pendings, req.Key)
		delete(owned, req.Key)
		if p.epoch == s.epoch {
			s.cooldowns[req.Key] = cooldown{until: now.Add(DenyCooldown), message: req.Message}
		}
		close(p.done)
	}
	return response{State: stateOK}
}

func (s *Server) lock() response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.entries = map[string]*entry{}
	s.cooldowns = map[string]cooldown{}
	for key, p := range s.pendings {
		delete(s.pendings, key)
		close(p.done)
	}
	return response{State: stateOK}
}

func (s *Server) status() response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.Now())
	entries := make([]EntryStatus, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, EntryStatus{Service: e.service, ManifestName: e.manifestName, Generation: e.generation, IdleUntil: e.idleUntil, HardUntil: e.hardUntil})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Generation < entries[j].Generation })
	return response{State: stateOK, Entries: entries}
}

func (s *Server) waiters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiting
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// randomGeneration starts each broker somewhere new, so a generation from a
// previous broker cannot match, and evict, a key fetched since it restarted.
func randomGeneration() uint64 {
	data := make([]byte, 8)
	_, _ = rand.Read(data)
	return binary.BigEndian.Uint64(data) >> 1
}

// readFrame reads one line, refusing anything larger than a frame can be.
func readFrame(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxFrame {
			return nil, errors.New("frame too large")
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

func newLease() string {
	data := make([]byte, 16)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

// RunBroker holds the broker lock for its lifetime, so exactly one broker
// serves a home. Only the lock holder may unlink a leftover socket; if another
// broker holds it, this one exits quietly.
func RunBroker(ctx context.Context, home string) error {
	if !Supported {
		return ErrUnsupported
	}
	dir, err := runtimeDir(home)
	if err != nil {
		return err
	}
	if err = ensurePrivateDir(dir); err != nil {
		return err
	}
	release, held, err := tryLockFile(filepath.Join(dir, lockName))
	if err != nil {
		return err
	}
	if !held {
		return nil
	}
	defer release()
	hardenBrokerProcess()
	path := filepath.Join(dir, socketName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("%s exists and is not a socket", path)
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	return NewServer().Serve(ctx, listener)
}
