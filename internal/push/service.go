package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Options configures Service. VAPID may be nil: push is then "not configured", every
// route says so and nothing is ever sent.
type Options struct {
	DB     *sql.DB
	Box    *secrets.Box
	VAPID  *VAPID
	Client *http.Client // egress client; nil selects SafeClient
	Now    func() time.Time
	// Backoff is the wait before retrying a transient failure; tests shorten it.
	Backoff func(attempt int) time.Duration
	// ValidateEndpoint replaces ValidEndpoint; tests use it to point at a local server.
	ValidateEndpoint func(string) error
}

const (
	sendAttempts = 3
	// deliveryRetention bounds push_delivery_events; it only has to outlast a re-reported terminal status.
	deliveryRetention = 30 * 24 * time.Hour
)

// Service owns the push routes and the completion notifier.
type Service struct {
	db       *sql.DB
	box      *secrets.Box
	sender   *Sender
	vapid    *VAPID
	now      func() time.Time
	backoff  func(int) time.Duration
	validate func(string) error

	base context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Box == nil {
		return nil, errors.New("push: database and secret box are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Backoff == nil {
		o.Backoff = func(n int) time.Duration { return time.Duration(1<<n) * time.Second }
	}
	if o.ValidateEndpoint == nil {
		o.ValidateEndpoint = ValidEndpoint
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{db: o.DB, box: o.Box, vapid: o.VAPID, now: o.Now, backoff: o.Backoff, validate: o.ValidateEndpoint, base: ctx, stop: cancel}
	if o.VAPID != nil {
		s.sender = NewSender(o.VAPID, o.Client)
		s.sender.now = o.Now
	}
	return s, nil
}

// Close cancels in-flight sends and waits for them.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

// Wait blocks until every notification started so far has been attempted.
func (s *Service) Wait() { s.wg.Wait() }

// ---- the notifier ---------------------------------------------------------------------

// RunFinished is called by the chat service after a run's terminal status is stored.
// status is that stored status: only COMPLETED, FAILED and TIMED_OUT notify. A
// cancelled run was the user's own act, and DISCONNECTED is "cannot tell", so neither
// is ever reported as a result. It returns at once; the send is bounded and detached.
func (s *Service) RunFinished(userID, runID, chatID, status string) {
	if s.sender == nil {
		return
	}
	var kind string
	switch status {
	case store.RunCompleted:
		kind = KindCompleted
	case store.RunFailed, store.RunTimedOut:
		kind = KindFailed
	default:
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.notify(userID, runID, chatID, kind)
	}()
}

func (s *Service) notify(userID, runID, chatID, kind string) {
	ctx, cancel := context.WithTimeout(s.base, 2*time.Minute)
	defer cancel()
	if err := store.PrunePushDeliveries(ctx, s.db, userID, s.now().Add(-deliveryRetention)); err != nil {
		log.Printf("push: prune delivery records: %v", err)
	}
	// Only a user who is still APPROVED with a READY binding gets anything.
	if _, err := store.ActiveBinding(ctx, s.db, userID); err != nil {
		return
	}
	mode, err := store.PushMode(ctx, s.db, userID)
	if err != nil || mode == store.PushOff || (mode == store.PushFailures && kind != KindFailed) {
		return
	}
	subs, err := store.ListPushSubs(ctx, s.db, userID)
	if err != nil {
		log.Printf("push: list subscriptions: %v", err)
		return
	}
	p := Payload{V: 1, Kind: kind, ChatID: chatID}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		ok, err := store.ClaimPushDelivery(ctx, s.db, userID, sub.ID, runID, kind, s.now())
		if err != nil || !ok {
			continue // already sent (or being sent) for this device
		}
		t, err := s.open(sub)
		if err != nil {
			// Typically a rotated ANTARES_SECRET_KEY. The error names the box, never the endpoint.
			log.Printf("push: cannot open subscription %s: %v", sub.ID, err)
			continue
		}
		err = s.sendWithRetry(ctx, t, p)
		switch {
		case err == nil:
			_ = store.MarkPushDelivered(ctx, s.db, userID, sub.ID, runID, kind, s.now())
		case errors.Is(err, ErrGone):
			_ = store.DeletePushSub(ctx, s.db, userID, sub.ID)
		default:
			// The claim stays: delivered_at is NULL, and a re-reported terminal status does not
			// send again. Delivery is at most once per {device, run, kind}, not guaranteed.
			// A claim without delivered_at is a notification that was not delivered; it is never retried
			// for this run (docs/runbooks/push.md). retryable says whether the attempt budget was spent
			// on a transient failure (true) or the push service refused it outright (false).
			log.Printf("push: delivery to %s failed (retryable=%v): %v", sub.ID, errors.Is(err, ErrTransient), err)
		}
	}
}

func (s *Service) sendWithRetry(ctx context.Context, t Target, p Payload) error {
	var err error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		if err = s.sender.Send(ctx, t, p); !errors.Is(err, ErrTransient) || attempt == sendAttempts-1 {
			return err
		}
		select {
		case <-time.After(s.backoff(attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (s *Service) open(sub store.PushSub) (Target, error) {
	ep, err := s.box.Open(sub.SealedEndpoint, sub.ID+"/endpoint")
	if err != nil {
		return Target{}, err
	}
	raw, err := s.box.Open(sub.SealedKeys, sub.ID+"/keys")
	if err != nil {
		return Target{}, err
	}
	var k struct{ P256dh, Auth []byte }
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		return Target{}, err
	}
	return Target{Endpoint: ep, P256dh: k.P256dh, Auth: k.Auth}, nil
}

// ---- HTTP -------------------------------------------------------------------------------

// Routes are mounted by the identity layer behind the Active guard and the
// same-origin check, exactly as the chat routes are.
func (s *Service) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/push/public-key":               http.HandlerFunc(s.publicKey),
		"POST /api/push/subscriptions":           http.HandlerFunc(s.subscribe),
		"DELETE /api/push/subscriptions/{subId}": http.HandlerFunc(s.unsubscribe),
		"GET /api/me/notifications":              http.HandlerFunc(s.getNotifications),
		"PUT /api/me/notifications":              http.HandlerFunc(s.putNotifications),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg, "requestId": "req_" + hex.EncodeToString(b)}})
}

func notConfigured(w http.ResponseWriter) {
	apiError(w, http.StatusNotImplemented, "FEATURE_NOT_CONFIGURED", "Notifications are not set up on this server")
}

func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("push: %s: %v", what, err)
	apiError(w, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
}

// owner is the signed-in account. The Active guard has run before any handler, so a miss here
// is a wiring mistake; it is answered, not assumed away.
func owner(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		apiError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in")
	}
	return u, ok
}

func (s *Service) publicKey(w http.ResponseWriter, r *http.Request) {
	if s.vapid == nil {
		notConfigured(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": s.vapid.PublicKey()})
}

type deviceView struct {
	ID           string `json:"id"`
	EndpointHash string `json:"endpointHash"`
	CreatedAt    string `json:"createdAt"`
}

type notificationsView struct {
	Configured bool         `json:"configured"`
	Mode       string       `json:"mode"`
	Devices    []deviceView `json:"devices"`
}

func (s *Service) getNotifications(w http.ResponseWriter, r *http.Request) {
	u, ok := owner(w, r)
	if !ok {
		return
	}
	mode, err := store.PushMode(r.Context(), s.db, u.ID)
	if err != nil {
		internalError(w, "read mode", err)
		return
	}
	subs, err := store.ListPushSubs(r.Context(), s.db, u.ID)
	if err != nil {
		internalError(w, "list devices", err)
		return
	}
	v := notificationsView{Configured: s.vapid != nil, Mode: mode, Devices: []deviceView{}}
	for _, sub := range subs {
		v.Devices = append(v.Devices, deviceView{ID: sub.ID, EndpointHash: sub.EndpointHash, CreatedAt: sub.CreatedAt})
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Service) putNotifications(w http.ResponseWriter, r *http.Request) {
	u, ok := owner(w, r)
	if !ok {
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch in.Mode {
	case store.PushOff, store.PushAll, store.PushFailures:
	default:
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "mode must be off, all or failures")
		return
	}
	if s.vapid == nil && in.Mode != store.PushOff {
		notConfigured(w)
		return
	}
	if err := store.SetPushMode(r.Context(), s.db, u.ID, in.Mode, s.now()); err != nil {
		internalError(w, "set mode", err)
		return
	}
	s.getNotifications(w, r)
}

// decode reads a small JSON body and rejects any field it does not know: a request
// that carries a profile, key or session id is refused, not ignored.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed request")
		return false
	}
	return true
}

func (s *Service) subscribe(w http.ResponseWriter, r *http.Request) {
	u, ok := owner(w, r)
	if !ok {
		return
	}
	if s.vapid == nil {
		notConfigured(w)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !decode(w, r, &in) {
		return
	}
	p256dh, err1 := base64.RawURLEncoding.DecodeString(in.Keys.P256dh)
	authSecret, err2 := base64.RawURLEncoding.DecodeString(in.Keys.Auth)
	if err1 != nil || err2 != nil || len(authSecret) != 16 {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed subscription keys")
		return
	}
	if _, err := ecdh.P256().NewPublicKey(p256dh); err != nil {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed subscription keys")
		return
	}
	if err := s.validate(in.Endpoint); err != nil {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "That push endpoint is not accepted")
		return
	}
	id := store.NewID()
	sealedEP, err := s.box.Seal(in.Endpoint, id+"/endpoint")
	if err != nil {
		internalError(w, "seal endpoint", err)
		return
	}
	keys, _ := json.Marshal(struct{ P256dh, Auth []byte }{p256dh, authSecret})
	sealedKeys, err := s.box.Seal(string(keys), id+"/keys")
	if err != nil {
		internalError(w, "seal keys", err)
		return
	}
	sum := sha256.Sum256([]byte(in.Endpoint))
	_, err = store.SavePushSub(r.Context(), s.db, store.PushSub{ID: id, UserID: u.ID, EndpointHash: hex.EncodeToString(sum[:]),
		SealedEndpoint: sealedEP, SealedKeys: sealedKeys}, s.now())
	switch {
	case errors.Is(err, store.ErrEndpointHeld):
		apiError(w, http.StatusConflict, "ENDPOINT_IN_USE", "This browser subscription is already in use; create a new one")
		return
	case errors.Is(err, store.ErrTooManyDevices):
		apiError(w, http.StatusConflict, "TOO_MANY_DEVICES", "Remove a device before adding another")
		return
	case err != nil:
		internalError(w, "save subscription", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Service) unsubscribe(w http.ResponseWriter, r *http.Request) {
	u, ok := owner(w, r)
	if !ok {
		return
	}
	err := store.DeletePushSub(r.Context(), s.db, u.ID, r.PathValue("subId"))
	switch {
	case errors.Is(err, store.ErrNoSubscription):
		apiError(w, http.StatusNotFound, "NOT_FOUND", "Not found")
	case err != nil:
		internalError(w, "delete subscription", err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}
