package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type mockRepo struct {
	tasks map[string]*Task
}

func newMockRepo() *mockRepo {
	return &mockRepo{tasks: make(map[string]*Task)}
}

func (m *mockRepo) Create(task *Task) error {
	m.tasks[task.ID] = task
	return nil
}

func (m *mockRepo) GetAll() ([]*Task, error) {
	result := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		result = append(result, t)
	}
	return result, nil
}

func (m *mockRepo) GetByID(id string) (*Task, error) {
	t, ok := m.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return t, nil
}

func (m *mockRepo) Update(task *Task) error {
	if _, ok := m.tasks[task.ID]; !ok {
		return ErrTaskNotFound
	}
	m.tasks[task.ID] = task
	return nil
}

func (m *mockRepo) Delete(id string) error {
	if _, ok := m.tasks[id]; !ok {
		return ErrTaskNotFound
	}
	delete(m.tasks, id)
	return nil
}

func (m *mockRepo) SearchByTitle(title string) ([]*Task, error) {
	var result []*Task
	for _, t := range m.tasks {
		if t.Title == title {
			result = append(result, t)
		}
	}
	return result, nil
}

func newTestService(repo *mockRepo) *TaskService {
	log, _ := zap.NewDevelopment()
	return NewTaskService(repo, nil, log, 120*time.Second, 30*time.Second, nil)
}

func TestCreate(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	task, err := svc.Create(context.Background(), CreateTaskRequest{
		Title:       "Test Task",
		Description: "Description",
		DueDate:     "2026-12-31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Title != "Test Task" {
		t.Errorf("expected title 'Test Task', got '%s'", task.Title)
	}
	if task.Done {
		t.Error("new task should not be done")
	}
	if task.ID == "" {
		t.Error("task ID should not be empty")
	}
	if len(repo.tasks) != 1 {
		t.Errorf("expected 1 task in repo, got %d", len(repo.tasks))
	}
}

func TestGetAll(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	svc.Create(context.Background(), CreateTaskRequest{Title: "Task 1"})
	svc.Create(context.Background(), CreateTaskRequest{Title: "Task 2"})

	tasks, err := svc.GetAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestGetByID(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	created, _ := svc.Create(context.Background(), CreateTaskRequest{Title: "Find Me"})

	found, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.Title != "Find Me" {
		t.Errorf("expected title 'Find Me', got '%s'", found.Title)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	_, err := svc.GetByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	created, _ := svc.Create(context.Background(), CreateTaskRequest{Title: "Original"})

	newTitle := "Updated"
	done := true
	updated, err := svc.Update(context.Background(), created.ID, UpdateTaskRequest{
		Title: &newTitle,
		Done:  &done,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Title != "Updated" {
		t.Errorf("expected title 'Updated', got '%s'", updated.Title)
	}
	if !updated.Done {
		t.Error("expected task to be done")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	newTitle := "X"
	_, err := svc.Update(context.Background(), "nonexistent", UpdateTaskRequest{Title: &newTitle})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	created, _ := svc.Create(context.Background(), CreateTaskRequest{Title: "Delete Me"})

	err := svc.Delete(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(repo.tasks))
	}
}

func TestDelete_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	err := svc.Delete(context.Background(), "nonexistent")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestSearchByTitle(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	svc.Create(context.Background(), CreateTaskRequest{Title: "Alpha"})
	svc.Create(context.Background(), CreateTaskRequest{Title: "Beta"})
	svc.Create(context.Background(), CreateTaskRequest{Title: "Alpha"})

	results, err := svc.SearchByTitle("Alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestUpdate_PartialFields(t *testing.T) {
	repo := newMockRepo()
	svc := newTestService(repo)

	created, _ := svc.Create(context.Background(), CreateTaskRequest{
		Title:       "Original",
		Description: "Desc",
		DueDate:     "2026-01-01",
	})

	newDesc := "New Desc"
	updated, err := svc.Update(context.Background(), created.ID, UpdateTaskRequest{Description: &newDesc})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Title != "Original" {
		t.Errorf("title should remain 'Original', got '%s'", updated.Title)
	}
	if updated.Description != "New Desc" {
		t.Errorf("expected description 'New Desc', got '%s'", updated.Description)
	}
}
