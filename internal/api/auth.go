package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/auth"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const sessionCookie = "hims_session"
const sessionTTL = 12 * time.Hour

// identity is the authenticated principal attached to a request context.
type identity struct {
	UserID   uuid.UUID
	Username string
	SiteID   *uuid.UUID // location scope; nil = global (all sites)
	Perms    map[string]bool
	Admin    bool
}

func (i *identity) can(perm string) bool {
	if i == nil {
		return true // open mode (no identity) — see authMiddleware safety valve
	}
	return i.Admin || i.Perms[perm]
}

type ctxKey int

const idKey ctxKey = iota + 1

func identityFrom(ctx context.Context) (*identity, bool) {
	v, ok := ctx.Value(idKey).(*identity)
	return v, ok
}

// authMiddleware enforces that every /api/v1 request carries a valid session,
// except the login endpoint and the public OpenAPI spec. Safety valve: while NO
// user has a password set yet (fresh install before bootstrap), it runs in OPEN
// mode so the system isn't bricked; the first password activates enforcement.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasSuffix(p, "/auth/login") || strings.HasSuffix(p, "/openapi.json") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authActive.Load() {
			next.ServeHTTP(w, r) // open mode: no enforcement until a password exists
			return
		}
		id := s.resolveSession(r)
		if id == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), idKey, id)))
	})
}

// resolveSession reads the session cookie and loads the identity, or nil.
func (s *Server) resolveSession(r *http.Request) *identity {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	th := auth.HashToken(c.Value)
	sess, err := s.queries.GetSession(r.Context(), th)
	if err != nil || !sess.IsActive {
		return nil
	}
	_ = s.queries.TouchSession(r.Context(), th)
	perms := map[string]bool{}
	if codes, err := s.queries.PermissionsForUser(r.Context(), sess.UserID); err == nil {
		for _, c := range codes {
			perms[c] = true
		}
	}
	return &identity{
		UserID: sess.UserID, Username: sess.Username, SiteID: sess.LocationID,
		Perms: perms, Admin: perms["rbac.manage"],
	}
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.queries.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !u.IsActive || !auth.CheckPassword(u.PasswordHash, req.Password) {
		// Uniform error — never reveal which of username/password was wrong.
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.queries.CreateSession(r.Context(), db.CreateSessionParams{
		TokenHash: auth.HashToken(token), UserID: u.ID, ExpiresAt: time.Now().Add(sessionTTL),
	}); err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(sessionTTL),
	})
	s.auditAs(u.Username, r, "user", "auth.login", "user", u.ID.String(), "Logged in", nil)
	s.writeMe(w, r.Context(), u.ID, u.Username, u.LocationID)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.queries.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if id, ok := identityFrom(r.Context()); ok {
		s.writeMe(w, r.Context(), id.UserID, id.Username, id.SiteID)
		return
	}
	// Open mode (no enforcement yet) — tell the UI auth isn't active.
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "auth_active": s.authActive.Load()})
}

// writeMe emits the current principal: identity, permission codes, site scope.
func (s *Server) writeMe(w http.ResponseWriter, ctx context.Context, uid uuid.UUID, username string, site *uuid.UUID) {
	var perms []string
	if codes, err := s.queries.PermissionsForUser(ctx, uid); err == nil {
		perms = codes
	}
	admin := false
	for _, c := range perms {
		if c == "rbac.manage" {
			admin = true
		}
	}
	siteID := ""
	if site != nil {
		siteID = site.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true, "auth_active": s.authActive.Load(),
		"user_id": uid.String(), "username": username,
		"permissions": perms, "admin": admin, "site_id": siteID,
	})
}

type changePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword lets the logged-in user rotate their own password.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := identityFrom(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var req changePasswordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 8 {
		http.Error(w, "new password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	u, err := s.queries.GetUserByUsername(r.Context(), id.Username)
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.CurrentPassword) {
		http.Error(w, "current password is incorrect", http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.queries.SetUserPassword(r.Context(), db.SetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.DeleteUserSessions(r.Context(), u.ID) // force re-login elsewhere
	s.auditAs(id.Username, r, "user", "auth.password_change", "user", u.ID.String(), "Changed own password", nil)
	w.WriteHeader(http.StatusNoContent)
}

// BootstrapAdmin ensures a usable admin login exists. If no user has a password
// yet and an admin user/password is supplied, it creates (or adopts) that user,
// grants it an 'admin' role holding every permission, sets the password, and
// activates auth enforcement. Called once at startup.
func (s *Server) BootstrapAdmin(ctx context.Context, username, password string) error {
	n, err := s.queries.CountUsersWithPassword(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.authActive.Store(true) // passwords exist → enforce
		return nil
	}
	if username == "" || password == "" {
		return nil // nothing to bootstrap; stays in open mode
	}
	// Find or create the admin user.
	u, err := s.queries.GetUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		created, cerr := s.queries.CreateUser(ctx, db.CreateUserParams{Username: username, FullName: "Administrator", IsActive: true})
		if cerr != nil {
			return cerr
		}
		u, err = s.queries.GetUserByUsername(ctx, created.Username)
	}
	if err != nil {
		return err
	}
	// Ensure permission catalog + an 'admin' role with all permissions.
	for _, p := range standardPermissions {
		_, _ = s.queries.CreatePermission(ctx, db.CreatePermissionParams{Code: p.code, Description: p.desc})
	}
	role, rerr := s.queries.CreateRole(ctx, db.CreateRoleParams{Name: "admin", Description: "Full access (bootstrap)"})
	if rerr != nil {
		// Role may already exist — look it up.
		roles, lerr := s.queries.ListRoles(ctx)
		if lerr != nil {
			return rerr
		}
		for _, rl := range roles {
			if rl.Name == "admin" {
				role = rl
			}
		}
	}
	perms, _ := s.queries.ListPermissions(ctx)
	for _, p := range perms {
		_ = s.queries.AddRolePermission(ctx, db.AddRolePermissionParams{RoleID: role.ID, PermissionID: p.ID})
	}
	_ = s.queries.AddUserRole(ctx, db.AddUserRoleParams{UserID: u.ID, RoleID: role.ID})
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.queries.SetUserPassword(ctx, db.SetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
		return err
	}
	s.authActive.Store(true)
	return nil
}

// StartSessionGC periodically purges expired sessions.
func (s *Server) StartSessionGC(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = s.queries.DeleteExpiredSessions(ctx)
			}
		}
	}()
}
