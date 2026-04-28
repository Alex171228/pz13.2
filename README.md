# Практическое задание 11
## Шишков А.Д. ЭФМО-02-22
## Тема
Создание GraphQL API с использованием gqlgen. Запросы и мутации.

## Цель
Освоить разработку GraphQL API на Go с использованием библиотеки gqlgen, определить GraphQL-схему, реализовать резолверы для операций CRUD, интегрировать GraphQL в существующий сервис tasks и проверить работу через Playground.

---

## 1. Что такое GraphQL

**GraphQL** — язык запросов к API и среда выполнения, позволяющая клиенту точно указать, какие данные ему нужны. В отличие от REST, где каждый ресурс — отдельный endpoint, в GraphQL один endpoint (`/query`) принимает все запросы.

Основные преимущества:
- Решение проблемы **over-fetching** (сервер возвращает только запрошенные поля) и **under-fetching** (один запрос вместо нескольких).
- Единый endpoint для всех операций.
- Строгая типизация через **схему**.

---

## 2. Основные концепции GraphQL

- **type** — описание структуры объекта (например, `Task`).
- **input** — входной тип для передачи данных в мутации.
- **Query** — операции **чтения** данных.
- **Mutation** — операции **изменения** данных (создание, обновление, удаление).
- **Resolver** — функция, которая выполняет логику для конкретного поля схемы.

---

## 3. Что такое gqlgen

**gqlgen** — библиотека для Go, реализующая **schema-first** подход: разработчик описывает схему в `.graphqls`, после чего генератор создаёт типобезопасный Go-код (модели, интерфейсы резолверов). Остаётся только реализовать бизнес-логику в резолверах.

---

## 4. GraphQL-схема

Файл `services/tasks/graph/schema.graphqls`:

```graphql
type Task {
  id: ID!
  title: String!
  description: String
  due_date: String
  done: Boolean!
  created_at: String
}

type Query {
  tasks: [Task!]!
  task(id: ID!): Task
}

type Mutation {
  createTask(input: CreateTaskInput!): Task!
  updateTask(id: ID!, input: UpdateTaskInput!): Task!
  deleteTask(id: ID!): Boolean!
}

input CreateTaskInput {
  title: String!
  description: String
  due_date: String
}

input UpdateTaskInput {
  title: String
  description: String
  due_date: String
  done: Boolean
}
```

---

## 5. Интеграция с существующим сервисом

GraphQL **встроен** в существующий сервис **tasks** (порт **8082**). REST-эндпоинты (`/v1/tasks/...`) и GraphQL (`POST /query`, `GET /` — Playground) работают **в одном процессе**, используют один `TaskService`, одну базу PostgreSQL, один кэш Redis.

Резолверы вызывают методы `TaskService`:
- `tasks` → `TaskService.GetAll()`
- `task(id)` → `TaskService.GetByID(ctx, id)`
- `createTask(input)` → `TaskService.Create(CreateTaskRequest{...})`
- `updateTask(id, input)` → `TaskService.Update(ctx, id, UpdateTaskRequest{...})`
- `deleteTask(id)` → `TaskService.Delete(ctx, id)`

Endpoint `/query` защищён **JWT-авторизацией** через тот же `AuthVerifier`, что и REST.

---

## 6. Структура файлов GraphQL

```
services/tasks/graph/
  schema.graphqls         — GraphQL-схема
  resolver.go             — корневой Resolver (хранит *TaskService)
  schema.resolvers.go     — реализация Query/Mutation
  generated.go            — автогенерация gqlgen (не редактируется)
  model/models_gen.go     — сгенерированные модели

gqlgen.yml                — конфигурация генератора (корень репозитория)
```

---

## 7. Запуск

```bash
# auth на хосте (если не запущен)
go run ./services/auth/cmd/auth

# tasks с GraphQL (Postgres должен быть доступен)
go run ./services/tasks/cmd/tasks
```

Playground: **http://localhost:8082/**
GraphQL endpoint: **POST http://localhost:8082/query**

---

## 8. Проверки через Playground

Перед выполнением мутаций и запросов добавьте в Playground заголовок авторизации (вкладка **HTTP Headers** внизу):

```json
{
  "Authorization": "Bearer <token>"
}
```

Токен получить: `POST http://localhost:8081/v1/auth/login` с телом `{"username":"student","password":"student"}`.

### Получить все задачи

```graphql
query {
  tasks {
    id
    title
    done
  }
}
```

![Список задач](docs/images/pz11_tasks.png)

### Получить задачу по ID

```graphql
query GetTask($id: ID!) {
  task(id: $id) {
    id
    title
    description
    done
  }
}
```

Переменные:

```json
{
  "id": "t_001"
}
```

![Задача по ID](docs/images/pz11_task_by_id.png)

### Создать задачу

```graphql
mutation Create($input: CreateTaskInput!) {
  createTask(input: $input) {
    id
    title
    description
    done
  }
}
```

Переменные:

```json
{
  "input": {
    "title": "Изучить GraphQL",
    "description": "Практическое занятие №11"
  }
}
```

![Создание задачи](docs/images/pz11_create.png)

### Обновить задачу

```graphql
mutation Update($id: ID!, $input: UpdateTaskInput!) {
  updateTask(id: $id, input: $input) {
    id
    title
    description
    done
  }
}
```

Переменные:

```json
{
  "id": "t_001",
  "input": {
    "done": true
  }
}
```

![Обновление задачи](docs/images/pz11_update.png)

### Удалить задачу

```graphql
mutation Delete($id: ID!) {
  deleteTask(id: $id)
}
```

Переменные:

```json
{
  "id": "t_002"
}
```

![Удаление задачи](docs/images/pz11_delete.png)

---

## 9. CI

В **GitHub Actions** добавлена проверка синтаксиса конфигурации NGINX: `nginx -t` в контейнере с примонтированным `deploy/lb/nginx.conf`. Для прохождения проверки в контейнер добавлены записи `--add-host=tasks_*:127.0.0.1`. Без реального upstream NGINX не резолвит имена при `nginx -t`.

Локально:

```bash
docker run --rm \
  --add-host=tasks_1:127.0.0.1 --add-host=tasks_2:127.0.0.1 --add-host=tasks_3:127.0.0.1 \
  -v "$(pwd)/deploy/lb/nginx.conf:/etc/nginx/nginx.conf:ro" \
  nginx:1.27-alpine nginx -t
```
