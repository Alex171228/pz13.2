package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"pz1.2/services/tasks/internal/client/authclient"
	"pz1.2/services/tasks/internal/service"
	"pz1.2/shared/logger"
)

type Handler struct {
	taskService  *service.TaskService
	authVerifier authclient.AuthVerifier
	log          *zap.Logger
	instanceID   string
}

func NewHandler(taskService *service.TaskService, authVerifier authclient.AuthVerifier, log *zap.Logger, instanceID string) *Handler {
	if instanceID == "" {
		instanceID = "tasks-default"
	}
	return &Handler{
		taskService:  taskService,
		authVerifier: authVerifier,
		log:          log,
		instanceID:   instanceID,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /whoami", h.handleWhoami)
	mux.HandleFunc("POST /v1/tasks", h.authMiddleware(h.handleCreate))
	mux.HandleFunc("GET /v1/tasks", h.authMiddleware(h.handleGetAll))
	mux.HandleFunc("GET /v1/tasks/search", h.authMiddleware(h.handleSearch))
	mux.HandleFunc("GET /v1/tasks/{id}", h.authMiddleware(h.handleGetByID))
	mux.HandleFunc("PATCH /v1/tasks/{id}", h.authMiddleware(h.handleUpdate))
	mux.HandleFunc("DELETE /v1/tasks/{id}", h.authMiddleware(h.handleDelete))
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"service":  "tasks",
		"instance": h.instanceID,
	})
}

func (h *Handler) handleWhoami(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]string{
		"instance": h.instanceID,
	})
}

func (h *Handler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := logger.FromContext(r.Context()).With(zap.String("component", "auth_middleware"))

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			l.Warn("missing authorization header")
			h.respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			l.Warn("invalid authorization format")
			h.respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization format"})
			return
		}

		token := parts[1]

		verifyResp, err := h.authVerifier.Verify(r.Context(), token)
		if err != nil {
			l.Error("auth service unavailable", zap.String("component", "auth_client"), zap.Error(err))
			h.respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth service unavailable"})
			return
		}

		if !verifyResp.Valid {
			l.Warn("invalid token", zap.Bool("has_auth", true))
			h.respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		l.Info("token verified", zap.String("subject", verifyResp.Subject))
		next(w, r)
	}
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))

	var req service.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		l.Warn("invalid request body", zap.Error(err))
		h.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Title == "" {
		l.Warn("title is required")
		h.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}

	task, err := h.taskService.Create(r.Context(), req)
	if err != nil {
		l.Error("failed to create task", zap.Error(err))
		h.respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	l.Info("task created", zap.String("task_id", task.ID))
	h.respondJSON(w, http.StatusCreated, task)
}

func (h *Handler) handleGetAll(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))

	tasks, err := h.taskService.GetAll()
	if err != nil {
		l.Error("failed to get tasks", zap.Error(err))
		h.respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	h.respondJSON(w, http.StatusOK, tasks)
}

func (h *Handler) handleGetByID(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))
	id := r.PathValue("id")

	task, err := h.taskService.GetByID(r.Context(), id)
	if err != nil {
		l.Warn("task not found", zap.String("task_id", id))
		h.respondJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	l.Debug("task retrieved", zap.String("task_id", id))
	h.respondJSON(w, http.StatusOK, task)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))
	id := r.PathValue("id")

	var req service.UpdateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		l.Warn("invalid request body", zap.Error(err))
		h.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	task, err := h.taskService.Update(r.Context(), id, req)
	if err != nil {
		l.Warn("task not found", zap.String("task_id", id))
		h.respondJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	l.Info("task updated", zap.String("task_id", id))
	h.respondJSON(w, http.StatusOK, task)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))
	id := r.PathValue("id")

	if err := h.taskService.Delete(r.Context(), id); err != nil {
		l.Warn("task not found", zap.String("task_id", id))
		h.respondJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	l.Info("task deleted", zap.String("task_id", id))
	w.Header().Set("X-Instance-ID", h.instanceID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	l := logger.FromContext(r.Context()).With(zap.String("component", "handler"))

	title := r.URL.Query().Get("title")
	if title == "" {
		l.Warn("search: title parameter required")
		h.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "title query parameter is required"})
		return
	}

	tasks, err := h.taskService.SearchByTitle(title)
	if err != nil {
		l.Error("search failed", zap.Error(err))
		h.respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	l.Info("search completed", zap.String("title", title), zap.Int("results", len(tasks)))
	h.respondJSON(w, http.StatusOK, tasks)
}

func AuthMiddlewareFunc(verifier authclient.AuthVerifier, log *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid authorization format"})
			return
		}

		resp, err := verifier.Verify(r.Context(), parts[1])
		if err != nil {
			log.Error("auth service unavailable", zap.Error(err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "auth service unavailable"})
			return
		}
		if !resp.Valid {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *Handler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Instance-ID", h.instanceID)
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}
