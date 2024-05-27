package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/berachain/offchain-sdk/log"
)

const defaultReadHeaderTimeout = 10 * time.Second

type Handler struct {
	Path    string
	Handler http.Handler
}

type Middleware func(http.Handler) http.Handler

type Server struct {
	cfg         *Config
	logger      log.Logger
	mux         *http.ServeMux
	srv         *http.Server
	closer      sync.Once
	middlewares []Middleware
}

func New(cfg *Config, logger log.Logger, middlewares ...Middleware) *Server {
	server := &Server{
		cfg:         cfg,
		logger:      logger,
		mux:         http.NewServeMux(),
		middlewares: middlewares,
	}
	return server
}

func (s *Server) RegisterHandler(h *Handler) {
	chain := h.Handler
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		chain = s.middlewares[i](chain)
	}
	s.mux.Handle(h.Path, chain)
}

func (s *Server) RegisterMiddleware(m Middleware) {
	s.middlewares = append(s.middlewares, m)
}

func (s *Server) Start(ctx context.Context) {
	s.srv = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.HTTP.Host, s.cfg.HTTP.Port),
		Handler:           s.mux,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
	}

	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("HTTP server errored", "err", err)
	} else {
		s.logger.Info("HTTP server closed")
	}
}

func (s *Server) Stop() {
	s.closer.Do(func() {
		if err := s.srv.Close(); err != nil {
			s.logger.Error("HTTP server close error", "err", err)
		}
	})
}
