package auth

import (
	"context"
	"net/http"
)

type contextKey string

const userContextKey contextKey = "user"

// Middleware checks for a valid JWT in the "token" cookie.
// If valid, the user is added to the request context.
// If invalid, redirects to /login.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("token")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		claims, err := s.ValidateToken(cookie.Value)
		if err != nil {
			http.SetCookie(w, &http.Cookie{
				Name:   "token",
				MaxAge: -1,
				Path:   "/",
			})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		user := &User{
			ID:       claims.UserID,
			Username: claims.Username,
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromContext extracts the authenticated user from the request context.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userContextKey).(*User)
	return u
}
