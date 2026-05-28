package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"pz1.2/services/tasks/internal/cache"
)

var (
	ErrTaskNotFound = errors.New("task not found")
)

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Done        bool   `json:"done"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type CreateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	DueDate     string `json:"due_date"`
}

type UpdateTaskRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`
	Done        *bool   `json:"done,omitempty"`
}

type TaskRepository interface {
	Create(task *Task) error
	GetAll() ([]*Task, error)
	GetByID(id string) (*Task, error)
	Update(task *Task) error
	Delete(id string) error
	SearchByTitle(title string) ([]*Task, error)
}

type TaskEventPublisher interface {
	PublishTaskCreated(ctx context.Context, taskID string) error
}

type TaskService struct {
	repo      TaskRepository
	redis     *redis.Client
	log       *zap.Logger
	ttl       time.Duration
	ttlJitter time.Duration
	publisher TaskEventPublisher
}

func NewTaskService(repo TaskRepository, redisClient *redis.Client, log *zap.Logger, ttl, ttlJitter time.Duration, publisher TaskEventPublisher) *TaskService {
	return &TaskService{
		repo:      repo,
		redis:     redisClient,
		log:       log,
		ttl:       ttl,
		ttlJitter: ttlJitter,
		publisher: publisher,
	}
}

func (s *TaskService) Create(ctx context.Context, req CreateTaskRequest) (*Task, error) {
	task := &Task{
		ID:          "t_" + uuid.New().String()[:8],
		Title:       req.Title,
		Description: req.Description,
		DueDate:     req.DueDate,
		Done:        false,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	if err := s.repo.Create(task); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		if err := s.publisher.PublishTaskCreated(ctx, task.ID); err != nil {
			s.log.Warn("task created but event publish failed", zap.String("task_id", task.ID), zap.Error(err))
		}
	}

	return task, nil
}

func (s *TaskService) GetAll() ([]*Task, error) {
	return s.repo.GetAll()
}

func (s *TaskService) GetByID(ctx context.Context, id string) (*Task, error) {
	key := cache.TaskByIDKey(id)

	if s.redis != nil {
		cached, err := s.redis.Get(ctx, key).Result()
		if err == nil {
			var t Task
			if err := json.Unmarshal([]byte(cached), &t); err == nil {
				s.log.Info("cache hit", zap.String("key", key))
				return &t, nil
			}
			s.log.Warn("cache decode error", zap.String("key", key), zap.Error(err))
		} else if errors.Is(err, redis.Nil) {
			s.log.Info("cache miss", zap.String("key", key))
		} else {
			s.log.Warn("redis read error", zap.String("key", key), zap.Error(err))
		}
	}

	task, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.redis != nil {
		bytes, err := json.Marshal(task)
		if err == nil {
			ttl := cache.TTLWithJitter(s.ttl, s.ttlJitter)
			if setErr := s.redis.Set(ctx, key, bytes, ttl).Err(); setErr != nil {
				s.log.Warn("redis write error", zap.String("key", key), zap.Error(setErr))
			} else {
				s.log.Info("cache set", zap.String("key", key), zap.Duration("ttl", ttl))
			}
		}
	}

	return task, nil
}

func (s *TaskService) Update(ctx context.Context, id string, req UpdateTaskRequest) (*Task, error) {
	task, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.DueDate != nil {
		task.DueDate = *req.DueDate
	}
	if req.Done != nil {
		task.Done = *req.Done
	}

	if err := s.repo.Update(task); err != nil {
		return nil, err
	}

	s.invalidateCache(ctx, id)
	return task, nil
}

func (s *TaskService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	s.invalidateCache(ctx, id)
	return nil
}

func (s *TaskService) SearchByTitle(title string) ([]*Task, error) {
	return s.repo.SearchByTitle(title)
}

func (s *TaskService) invalidateCache(ctx context.Context, id string) {
	if s.redis == nil {
		return
	}
	key := cache.TaskByIDKey(id)
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		s.log.Warn("redis delete error", zap.String("key", key), zap.Error(err))
	} else {
		s.log.Info("cache invalidated", zap.String("key", key))
	}
}
