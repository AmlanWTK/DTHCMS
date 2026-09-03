package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime/ws"
)

// The endpoint: `GET /v1/realtime`, upgraded to a WebSocket.
//
// **On the path.** The plan writes it `WSS /realtime`. It is mounted at `/v1/realtime`
// instead, and the deviation is deliberate: every other endpoint is versioned, and a
// long-lived protocol needs versioning more than a request does — a client that reconnects
// after a deployment must be able to tell that the shape of what it receives has changed.
// `wss` versus `ws` is the ingress's business, not the handler's.
//
// **Authentication** is the ordinary chain: Authenticate then VerifyDevice, mounted in
// front of this handler, so a socket is opened by exactly the credential an HTTP request
// would need. Nothing is read from the query string — a token in a URL is a token in an
// access log, in a Referer header, and in a browser's history.
//
// **The lifetime** is two goroutines: one reading commands, one writing envelopes. They
// meet only through the hub's bounded queue, which is what makes a slow reader a dropped
// message rather than a stalled server.

// Resolver turns an authenticated caller into the RBAC subject the filter uses. It is the
// same resolver the HTTP authorizer uses; the interface is here so realtime does not have
// to know how a subject is built.
type Resolver interface {
	Subject(ctx context.Context, userID, facilityID uuid.UUID, activeRole string) (rbac.Subject, error)
}

// HandlerConfig assembles the endpoint.
type HandlerConfig struct {
	Hub      *Hub
	Resolver Resolver
	Clock    clock.Clock
	Logger   *slog.Logger
	// AllowedOrigins are the browser origins that may open a connection. Same-origin is
	// always allowed; this is for the development server on another port.
	AllowedOrigins []string
	// Heartbeat is how often the server pings an idle connection. The read deadline is
	// two and a half times this, so one missed beat is tolerated and two are not.
	Heartbeat time.Duration
	// ReauthEvery is how often a live connection's subject is resolved again, so that a
	// revoked role stops applying without waiting for the socket to drop.
	ReauthEvery time.Duration
}

func (c *HandlerConfig) defaults() {
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = 25 * time.Second
	}
	if c.ReauthEvery <= 0 {
		c.ReauthEvery = time.Minute
	}
}

// Handler serves the gateway.
type Handler struct {
	cfg HandlerConfig
}

func NewHandler(cfg HandlerConfig) *Handler {
	cfg.defaults()
	return &Handler{cfg: cfg}
}

// ServeHTTP upgrades and runs the connection.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.cfg.Logger, errs.ErrUnauthenticated)
		return
	}
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, r, h.cfg.Logger, errs.ErrUnauthenticated)
		return
	}

	subject, err := h.cfg.Resolver.Subject(r.Context(), userID, facilityID, caller.ActiveRole)
	if err != nil {
		h.cfg.Logger.InfoContext(r.Context(), "realtime connection refused", "reason", err.Error())
		httpx.WriteError(w, r, h.cfg.Logger, errs.ErrForbidden)
		return
	}

	deviceID := uuid.Nil
	if caller.DeviceID != "" {
		deviceID, _ = uuid.Parse(caller.DeviceID)
	}

	conn, err := h.cfg.Hub.Register(subject, deviceID)
	if err != nil {
		// The process is full. 503 with Retry-After is the honest answer: come back, or go
		// to another instance.
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, h.cfg.Logger, errs.ErrUnavailable)
		return
	}

	socket, err := ws.Accept(w, r, ws.AcceptOptions{
		OriginPatterns: h.cfg.AllowedOrigins,
		MaxMessage:     32 << 10, // a command is small; anything larger is not one
		ReadTimeout:    h.cfg.Heartbeat*5/2 + time.Second,
		WriteTimeout:   10 * time.Second,
	})
	if err != nil {
		h.cfg.Hub.Unregister(conn, "handshake_failed")
		h.cfg.Logger.InfoContext(r.Context(), "realtime handshake refused", "error", err.Error())
		return
	}

	h.cfg.Logger.InfoContext(r.Context(), "realtime connection opened",
		"connection_id", conn.ID.String(), "user_id", userID.String(),
		"role", string(subject.ActiveRole), "remote", socket.RemoteAddr().String())

	session := &session{
		h: h, conn: conn, socket: socket,
		userID: userID, facilityID: facilityID, activeRole: caller.ActiveRole,
	}
	// The hub closes the socket when it drops the connection — an eviction, a shutdown —
	// so the read goroutine ends rather than blocking on a socket nobody owns any more.
	conn.OnClose(func(reason string) {
		_ = socket.Close(ws.StatusGoingAway, reason)
	})
	session.run(r.Context())
}

// session is one connection's two goroutines and the state they share.
type session struct {
	h          *Handler
	conn       *Connection
	socket     *ws.Conn
	userID     uuid.UUID
	facilityID uuid.UUID
	activeRole string
}

func (s *session) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	// The welcome carries the cursor, so a client that reconnected knows where it is even
	// if it lost its own record of it.
	s.h.cfg.Hub.Send(s.conn, Envelope{
		Type: "welcome", Cursor: s.conn.Cursor(), At: s.h.cfg.Clock.Now(),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.writeLoop(ctx)
	}()

	s.readLoop(ctx)
	cancel()
	s.h.cfg.Hub.Unregister(s.conn, "client_closed")
	<-done
	_ = s.socket.Close(ws.StatusNormalClosure, "")
}

// readLoop handles the client's commands until it closes or misbehaves.
func (s *session) readLoop(ctx context.Context) {
	for {
		op, payload, err := s.socket.Read()
		if err != nil {
			s.logClose(ctx, err)
			return
		}
		if op != ws.OpText {
			_ = s.socket.Close(ws.StatusUnsupportedData, "commands are JSON text")
			return
		}

		var command Command
		if err := json.Unmarshal(payload, &command); err != nil {
			s.h.cfg.Hub.Send(s.conn, Envelope{
				Type: "error", Error: "malformed_command",
				Detail: "a command is a JSON object with a type", At: s.h.cfg.Clock.Now(),
			})
			continue
		}
		s.handle(ctx, command)
	}
}

func (s *session) handle(ctx context.Context, command Command) {
	now := s.h.cfg.Clock.Now()
	switch command.Type {
	case "subscribe":
		added, refused, err := s.h.cfg.Hub.Subscribe(s.conn, command.Topics)
		if err != nil {
			s.h.cfg.Hub.Send(s.conn, Envelope{Type: "error", Error: "too_many_subscriptions", Detail: err.Error(), At: now})
			return
		}
		s.h.cfg.Hub.Send(s.conn, Envelope{Type: "subscribed", Topics: added, At: now})
		if len(refused) > 0 {
			// Named, not silent. A subscription that quietly delivers nothing is the
			// hardest kind of bug to find from the client's side.
			s.h.cfg.Hub.Send(s.conn, Envelope{
				Type: "refused", Topics: refused, Error: "not_permitted",
				Detail: "your role may not watch these topics", At: now,
			})
		}
	case "unsubscribe":
		s.h.cfg.Hub.Send(s.conn, Envelope{
			Type: "unsubscribed", Topics: s.h.cfg.Hub.Unsubscribe(s.conn, command.Topics), At: now,
		})
	case "resume":
		// The gateway does not replay: it has no store of past messages, and inventing one
		// would be a second copy of the ledger with different access rules. What `resume`
		// does is tell the client, precisely, what it must fetch — and the client then
		// pulls. That is criterion 3 and criterion 4 being the same mechanism.
		s.h.cfg.Hub.Send(s.conn, Envelope{
			Type: "resumed", Cursor: s.conn.Cursor(), Dropped: s.conn.Dropped(),
			Detail: "fetch everything after `cursor` over HTTP; the gateway does not replay",
			At:     now,
		})
	case "ping":
		s.h.cfg.Hub.Send(s.conn, Envelope{Type: "pong", At: now})
	default:
		s.h.cfg.Hub.Send(s.conn, Envelope{
			Type: "error", Error: "unknown_command", Detail: command.Type, At: now,
		})
	}
	_ = ctx
}

// writeLoop drains the queue, beats the heartbeat, and re-resolves the subject.
func (s *session) writeLoop(ctx context.Context) {
	heartbeat := time.NewTicker(s.h.cfg.Heartbeat)
	defer heartbeat.Stop()
	reauth := time.NewTicker(s.h.cfg.ReauthEvery)
	defer reauth.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.conn.Done():
			return
		case envelope, ok := <-s.conn.Out():
			if !ok {
				return
			}
			payload, err := marshal(envelope)
			if err != nil {
				s.h.cfg.Logger.ErrorContext(ctx, "realtime envelope not encodable", "error", err.Error())
				continue
			}
			if err := s.socket.Write(ws.OpText, payload); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := s.socket.Ping(nil); err != nil {
				return
			}
		case <-reauth.C:
			s.reauthenticate(ctx)
		}
	}
}

// reauthenticate resolves the subject again. A role revoked while a socket is open must
// stop applying, and the socket may be open for hours.
func (s *session) reauthenticate(ctx context.Context) {
	subject, err := s.h.cfg.Resolver.Subject(ctx, s.userID, s.facilityID, s.activeRole)
	if err != nil {
		s.h.cfg.Logger.InfoContext(ctx, "realtime connection no longer authorised; closing",
			"connection_id", s.conn.ID.String(), "reason", err.Error())
		// Written straight to the socket rather than queued: this goroutine is the one that
		// drains the queue, and it is about to stop. A queued envelope would be closed away
		// with the connection and the client would be dropped without being told why.
		if payload, err := marshal(Envelope{
			Type: "error", Error: "reauthentication_failed",
			Detail: "your access changed; reconnect", At: s.h.cfg.Clock.Now(),
		}); err == nil {
			_ = s.socket.Write(ws.OpText, payload)
		}
		s.h.cfg.Hub.Unregister(s.conn, "reauthentication_failed")
		return
	}
	s.conn.Reauthenticate(subject)
}

func (s *session) logClose(ctx context.Context, err error) {
	var closed ws.CloseError
	switch {
	case errors.As(err, &closed):
		s.h.cfg.Logger.InfoContext(ctx, "realtime connection closed by the client",
			"connection_id", s.conn.ID.String(), "status", int(closed.Status))
	case errors.Is(err, ws.ErrProtocol):
		s.h.cfg.Logger.InfoContext(ctx, "realtime connection violated the protocol",
			"connection_id", s.conn.ID.String(), "error", err.Error())
		_ = s.socket.Close(ws.StatusProtocolError, "")
	case errors.Is(err, ws.ErrTooLarge):
		_ = s.socket.Close(ws.StatusMessageTooBig, "")
	default:
		s.h.cfg.Logger.DebugContext(ctx, "realtime connection ended",
			"connection_id", s.conn.ID.String(), "error", err.Error())
	}
}
