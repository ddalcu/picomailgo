package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"picomailgo/internal/auth"
	"picomailgo/internal/config"
	"picomailgo/internal/db"
)

// MailSender sends email and saves to Sent folder.
type MailSender interface {
	Send(from, to string, raw []byte) error
	SaveToSent(userID int64, from, to, subject string, date time.Time, raw []byte) error
}

type Server struct {
	cfg       *config.Config
	db        *db.DB
	auth      *auth.Service
	mail      MailSender
	templates map[string]*template.Template
	mux       *http.ServeMux
}

func NewServer(cfg *config.Config, database *db.DB, authSvc *auth.Service, mailer MailSender, templateFS, staticFS fs.FS) (*Server, error) {
	templates, err := parseTemplates(templateFS)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:       cfg,
		db:        database,
		auth:      authSvc,
		mail:      mailer,
		templates: templates,
		mux:       http.NewServeMux(),
	}

	s.routes(staticFS)
	return s, nil
}

// parseTemplates builds a separate *template.Template for each page,
// each with its own "body" block definition that won't collide.
func parseTemplates(fsys fs.FS) (map[string]*template.Template, error) {
	pages := []string{
		"login.html", "register.html",
		"inbox.html", "message.html", "compose.html", "settings.html",
	}

	shared := []string{"layout.html", "partials/sidebar.html", "partials/message-list.html"}

	templates := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		files := append([]string{page}, shared...)
		tmpl, err := template.ParseFS(fsys, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		templates[page] = tmpl
	}

	// Standalone partial for HTMX endpoints
	partial, err := template.ParseFS(fsys, "partials/message-list.html")
	if err != nil {
		return nil, fmt.Errorf("parse message-list partial: %w", err)
	}
	templates["partials/message-list.html"] = partial

	return templates, nil
}

func (s *Server) routes(staticFS fs.FS) {
	// Static assets — no rate limiting
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Rate limit all non-static routes
	limiter := NewRateLimiter(120, time.Minute)

	s.mux.Handle("GET /login", limiter.Middleware(http.HandlerFunc(s.handleLoginPage)))
	s.mux.Handle("POST /login", limiter.Middleware(http.HandlerFunc(s.handleLogin)))
	s.mux.Handle("GET /register", limiter.Middleware(http.HandlerFunc(s.handleRegisterPage)))
	s.mux.Handle("POST /register", limiter.Middleware(http.HandlerFunc(s.handleRegister)))
	s.mux.Handle("GET /logout", limiter.Middleware(http.HandlerFunc(s.handleLogout)))

	protected := http.NewServeMux()
	protected.HandleFunc("GET /", s.handleRoot)
	protected.HandleFunc("GET /mailbox/{name}", s.handleMailbox)
	protected.HandleFunc("GET /message/{id}", s.handleMessage)
	protected.HandleFunc("GET /compose", s.handleComposePage)
	protected.HandleFunc("POST /compose", s.handleCompose)
	protected.HandleFunc("GET /settings", s.handleSettingsPage)
	protected.HandleFunc("POST /settings", s.handleSettings)
	protected.HandleFunc("GET /api/messages", s.handleMessageList)
	protected.HandleFunc("DELETE /api/messages/{id}", s.handleDeleteMessage)

	s.mux.Handle("/", limiter.Middleware(s.auth.Middleware(protected)))
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
