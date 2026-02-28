package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/ZenithSolitude/Hopefully/internal/db"
)

// ── Config ────────────────────────────────────────────────────────────────────

var secret []byte

func Init(key string) { secret = []byte(key) }

// ── User ──────────────────────────────────────────────────────────────────────

type User struct {
	ID        int64
	Username  string
	FullName  string
	Email     string
	IsAdmin   bool
	IsActive  bool
	CreatedAt string
	LastLogin string
}

func (u *User) DisplayName() string {
	if u.FullName != "" {
		return u.FullName
	}
	return u.Username
}

// ── Password ──────────────────────────────────────────────────────────────────

func HashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ── JWT ───────────────────────────────────────────────────────────────────────

type claims struct {
	UID int64 `json:"uid"`
	jwt.RegisteredClaims
}

func NewToken(userID int64, dur time.Duration) (string, error) {
	c := claims{
		UID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(dur)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

func ParseToken(s string) (int64, error) {
	tok, err := jwt.ParseWithClaims(s, &claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected method")
		}
		return secret, nil
	})
	if err != nil {
		return 0, err
	}
	c, ok := tok.Claims.(*claims)
	if !ok || !tok.Valid {
		return 0, errors.New("invalid token")
	}
	return c.UID, nil
}

// ── DB helpers ────────────────────────────────────────────────────────────────

func GetByID(id int64) (*User, error) {
	u := &User{}
	err := db.DB.QueryRow(
		`SELECT id, username, full_name, email, is_admin, is_active, created_at, COALESCE(last_login,'')
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.FullName, &u.Email, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLogin)
	return u, err
}

func GetByUsername(username string) (*User, string, error) {
	u := &User{}
	var hash string
	err := db.DB.QueryRow(
		`SELECT id, username, full_name, email, is_admin, is_active, created_at, COALESCE(last_login,''), password
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.FullName, &u.Email, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLogin, &hash)
	return u, hash, err
}

// ── Context ───────────────────────────────────────────────────────────────────

type ctxKey struct{}

func CtxSet(r *http.Request, u *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
}

func CtxGet(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

// ── Middleware ────────────────────────────────────────────────────────────────

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := cookieToken(r)
		if tok == "" {
			redirect(w, r)
			return
		}
		uid, err := ParseToken(tok)
		if err != nil {
			clearCookie(w)
			redirect(w, r)
			return
		}
		u, err := GetByID(uid)
		if err != nil || !u.IsActive {
			clearCookie(w)
			redirect(w, r)
			return
		}
		next.ServeHTTP(w, CtxSet(r, u))
	})
}

func AdminOnly(next http.Handler) http.Handler {
	return Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CtxGet(r).IsAdmin {
			http.Error(w, "403 Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func SetCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "tok",
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "tok", Path: "/", MaxAge: -1})
}

func cookieToken(r *http.Request) string {
	if c, err := r.Cookie("tok"); err == nil {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return h[7:]
	}
	return ""
}

func redirect(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}
