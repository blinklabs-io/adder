// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Handler exposes only authenticated configuration, health, and aggregate metrics.
// The owner must serve it on a private listener. Tokens are never logged.
func (r *Router) Handler(token string, source func() string) (http.Handler, error) {
	if len(token) < 32 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("operator token must contain at least 32 characters on one line")
	}
	expected := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			http.Error(w, "response encoding failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(append(data, '\n'))
	}
	mux.HandleFunc("GET /v1/config/status", func(w http.ResponseWriter, req *http.Request) {
		state := "unknown"
		if source != nil {
			state = source()
		}
		write(w, http.StatusOK, struct {
			Status
			Source string `json:"source"`
		}{r.Status(), state})
	})
	reload := func(w http.ResponseWriter, req *http.Request, id string) {
		data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 4096))
		if err != nil {
			write(w, http.StatusBadRequest, map[string]string{"error": "invalid reload request"})
			return
		}
		var result ReloadResult
		if id == "" {
			var body struct {
				Generation uint64 `json:"generation"`
			}
			err = decode(data, &body)
			if err == nil && body.Generation == 0 {
				err = errors.New("generation must be positive")
			}
			if err == nil {
				result, err = r.ReloadAll(req.Context(), body.Generation)
			}
		} else {
			var body struct {
				Revision uint64 `json:"revision"`
			}
			err = decode(data, &body)
			if err == nil {
				result, err = r.ReloadCustomer(req.Context(), id, body.Revision)
			}
		}
		if err != nil {
			write(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		write(w, http.StatusOK, result)
	}
	mux.HandleFunc("POST /v1/config/reload", func(w http.ResponseWriter, req *http.Request) { reload(w, req, "") })
	mux.HandleFunc("POST /v1/config/customers/{id}/reload", func(w http.ResponseWriter, req *http.Request) { reload(w, req, req.PathValue("id")) })
	mux.HandleFunc("GET /healthcheck", func(w http.ResponseWriter, req *http.Request) {
		s := r.Status()
		status := http.StatusOK
		if !s.Ready {
			status = http.StatusServiceUnavailable
		}
		write(w, status, map[string]bool{"ready": s.Ready})
	})
	mux.HandleFunc("GET /metrics", r.metrics)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		header := req.Header.Get("Authorization")
		supplied := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
		if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, req)
	}), nil
}
