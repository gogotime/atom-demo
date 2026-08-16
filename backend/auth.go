package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const cookieName = "atoms_session"
const tokenTTL = 7 * 24 * time.Hour

type Claims struct {
	UserID int64 `json:"uid"`
	jwt.RegisteredClaims
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func issueToken(userID int64) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", errors.New("JWT_SECRET not set")
	}
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(secret))
}

func parseToken(s string) (int64, error) {
	secret := os.Getenv("JWT_SECRET")
	t, err := jwt.ParseWithClaims(s, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return 0, err
	}
	c, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return 0, errors.New("invalid token")
	}
	return c.UserID, nil
}

func setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(tokenTTL.Seconds()),
	})
}

func clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func currentUser(r *http.Request) (*User, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, err
	}
	uid, err := parseToken(c.Value)
	if err != nil {
		return nil, err
	}
	return getUserByID(r.Context(), uid)
}

type ctxUserKey struct{}

func withUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := currentUser(r)
		if err != nil || u == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := withUser(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req registerReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if !strings.Contains(req.Email, "@") || len(req.Password) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email invalid or password too short (min 6)"})
		return
	}
	if existing, _ := getUserByEmail(ctx, req.Email); existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
		return
	}
	h, err := hashPassword(req.Password)
	if err != nil {
		log.Printf("hashPassword: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	id, err := createUser(ctx, req.Email, h)
	if err != nil {
		log.Printf("createUser: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	tok, err := issueToken(id)
	if err != nil {
		log.Printf("issueToken: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	setAuthCookie(w, tok)
	u, err := getUserByID(ctx, id)
	if err != nil || u == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req registerReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	u, err := getUserByEmail(ctx, req.Email)
	if err != nil || u == nil || !checkPassword(u.PasswordHash, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		return
	}
	tok, err := issueToken(u.ID)
	if err != nil {
		log.Printf("issueToken: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	setAuthCookie(w, tok)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	u, err := currentUser(r)
	if err != nil || u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}
