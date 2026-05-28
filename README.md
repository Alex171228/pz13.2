# Практическое задание 13
##  ЭФМО-02-25
## Тема

## Цель

---

## Краткое описание реализованного решения

В существующий проект интегрирован брокер сообщений RabbitMQ. После успешного создания задачи сервис `tasks` публикует событие `task.created` в очередь `task_events`. Отдельный сервис `worker` получает сообщение, декодирует JSON, выводит информацию о событии в лог и отправляет ручное подтверждение обработки через `Ack(false)`.

Публикация реализована в режиме **best effort**:

- задача считается созданной после успешной записи в PostgreSQL;
- ошибка публикации не приводит к откату создания задачи;
- ошибка публикации фиксируется в логах сервиса `tasks`.

---

## Используемые компоненты

- `services/tasks` — сервис задач, публикующий события в RabbitMQ;
- `services/worker` — отдельный consumer RabbitMQ;
- `services/auth` — сервис авторизации для HTTP-запросов к `tasks`;
- `Redis` — локальный кеш сервиса `tasks`;
- локальная инфраструктура RabbitMQ через `docker-compose`.

---

## Запуск RabbitMQ

Для работы с RabbitMQ используется Docker-контейнер с management-интерфейсом.

```yaml
version: "3.9"

services:
  rabbitmq:
    image: rabbitmq:3-management
    container_name: pz13-rabbitmq
    ports:
      - "5672:5672"
      - "15672:15672"
    environment:
      RABBITMQ_DEFAULT_USER: guest
      RABBITMQ_DEFAULT_PASS: guest
```

Запуск:

```bash
cd deploy/rabbit
docker compose up -d
docker compose ps
```

После запуска доступны:

- AMQP-порт `5672` для приложений;
- веб-интерфейс RabbitMQ Management UI: [http://localhost:15672](http://localhost:15672);
- логин и пароль: `guest / guest`.

<img width="1722" height="169" alt="image" src="https://github.com/user-attachments/assets/b2c320fe-edc9-4005-b5d0-568320287322" /> 

---

## Формат сообщения

Сообщение публикуется в формате JSON и содержит обязательные поля события:

```json
{
  "event": "task.created",
  "task_id": "t_a1b2c3d4",
  "ts": "2026-05-18T12:00:00Z"
}
```

В проекте также передаются дополнительные поля:

```json
{
  "event": "task.created",
  "task_id": "t_a1b2c3d4",
  "ts": "2026-05-18T12:00:00Z",
  "request_id": "pz13-001",
  "producer": "tasks",
  "version": "v1"
}
```

Структура события:

```go
type TaskEvent struct {
    Event     string `json:"event"`
    TaskID    string `json:"task_id"`
    TS        string `json:"ts"`
    RequestID string `json:"request_id,omitempty"`
    Producer  string `json:"producer,omitempty"`
    Version   string `json:"version,omitempty"`
}
```

Поле `event` определяет тип события.  
Поле `task_id` хранит идентификатор созданной задачи.  
Поле `ts` содержит временную метку события в формате UTC.

Сообщение публикуется с параметром `DeliveryMode: amqp.Persistent`, что повышает надёжность доставки при сбоях брокера.

---

## Очередь `task_events`

Очередь `task_events` объявляется как `durable` и используется как publisher, так и consumer.

Фрагмент объявления очереди:

```go
_, err := ch.QueueDeclare(
    queueName,
    true,
    false,
    false,
    false,
    nil,
)
```

Параметры объявления очереди:

- `durable = true`
- `autoDelete = false`
- `exclusive = false`

Это означает:

- очередь сохраняется после перезапуска RabbitMQ;
- очередь не удаляется автоматически;
- очередь не привязана к одному клиентскому соединению.

---

## Producer: публикация события в сервисе `tasks`

После успешного создания задачи сервис `tasks` публикует событие `task.created` в RabbitMQ.

Публикация выполняется после сохранения задачи в базе данных. Таким образом событие отражает уже состоявшийся факт создания задачи.

**Выбранный режим обработки ошибок:** `best effort`

- если RabbitMQ недоступен, создание задачи не отменяется;
- при ошибке публикации клиент всё равно получает успешный ответ;
- ошибка публикации только логируется.

Логика публикации после успешного сохранения задачи:

```go
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
            s.log.Warn("task created but event publish failed",
                zap.String("task_id", task.ID),
                zap.Error(err),
            )
        }
    }

    return task, nil
}
```

**Фрагмент публикации:**

```go
msg := events.TaskEvent{
    Event:     "task.created",
    TaskID:    taskID,
    TS:        time.Now().UTC().Format(time.RFC3339),
    RequestID: middleware.GetRequestID(ctx),
    Producer:  p.producer,
    Version:   p.version,
}

body, err := json.Marshal(msg)
if err != nil {
    return fmt.Errorf("marshal event: %w", err)
}

return ch.PublishWithContext(
    ctx,
    "",
    p.queueName,
    false,
    false,
    amqp091.Publishing{
        ContentType:  "application/json",
        DeliveryMode: amqp091.Persistent,
        Timestamp:    time.Now().UTC(),
        Body:         body,
    },
)
```

---

## Consumer: отдельный сервис `worker`

Для получения сообщений реализован отдельный сервис `worker`.

Основные характеристики consumer:

- очередь объявляется как `durable`;
- используется ручное подтверждение обработки;
- используется `prefetch = 1`;
- при некорректном сообщении выполняется `Nack(false, false)`.

**Фрагмент consumer:**

```go
msgs, err := ch.Consume(
    c.queueName,
    "",
    false,
    false,
    false,
    false,
    nil,
)

for {
    select {
    case d, ok := <-msgs:
        if !ok {
            return fmt.Errorf("delivery channel closed")
        }

        var ev events.TaskEvent
        if err := json.Unmarshal(d.Body, &ev); err != nil {
            c.log.Warn("bad message", zap.Error(err))
            d.Nack(false, false)
            continue
        }

        c.log.Info("task event received", zap.String("event", ev.Event))
        d.Ack(false)
    }
}
```

---

## Ручное подтверждение обработки (`ack`)

В `worker` используется ручное подтверждение обработки сообщения.

Это достигается за счёт параметра:

```go
autoAck = false
```

Сообщение подтверждается только после успешной обработки:

```go
if err := d.Ack(false); err != nil {
    c.log.Warn("ack failed", zap.Error(err))
}
```

Если сообщение не удаётся распарсить как JSON, оно отклоняется:

```go
_ = d.Nack(false, false)
```

Таким образом:

- успешно обработанное сообщение удаляется из очереди;
- невалидное сообщение не возвращается в очередь повторно.

---

## Использование prefetch

Для ограничения числа неподтвержденных сообщений используется настройка QoS:

```go
if err := ch.Qos(c.prefetch, 0, false); err != nil {
    return fmt.Errorf("configure qos: %w", err)
}
```

В проекте по умолчанию используется:

```text
WORKER_PREFETCH=1
```

Это означает, что RabbitMQ не будет отправлять worker больше одного неподтвержденного сообщения одновременно. Такое значение подходит для учебной демонстрации последовательной обработки сообщений.

---

## Переменные окружения

### Для сервиса `tasks`

- `TASKS_PORT` — порт сервиса `tasks`, по умолчанию `8082`
- `DATABASE_URL` — строка подключения к PostgreSQL
- `AUTH_MODE` — режим проверки авторизации: `http` или `grpc`
- `AUTH_BASE_URL` — адрес HTTP auth-сервиса
- `AUTH_GRPC_ADDR` — адрес gRPC auth-сервиса
- `REDIS_ADDR` — адрес Redis, по умолчанию `127.0.0.1:6379`
- `RABBIT_URL` — адрес RabbitMQ, по умолчанию `amqp://guest:guest@localhost:5672/`
- `QUEUE_NAME` — имя очереди, по умолчанию `task_events`

### Для сервиса `worker`

- `RABBIT_URL` — адрес RabbitMQ, по умолчанию `amqp://guest:guest@localhost:5672/`
- `QUEUE_NAME` — имя очереди, по умолчанию `task_events`
- `WORKER_PREFETCH` — значение `prefetch`, по умолчанию `1`

---

## Демонстрация работы

### Подготовка зависимостей

Перед запуском сервисов должны быть доступны:

- PostgreSQL на `5432`;
- Redis на `6379`;
- RabbitMQ на `5672` и `15672`.

### Запуск auth-сервиса

```bash
go run ./services/auth/cmd/auth
```

### Запуск worker

```bash
$env:RABBIT_URL="amqp://guest:guest@localhost:5672/"
$env:QUEUE_NAME="task_events"
$env:WORKER_PREFETCH="1"
go run ./services/worker/cmd/worker
```

### Запуск tasks

```bash
$env:RABBIT_URL="amqp://guest:guest@localhost:5672/"
$env:QUEUE_NAME="task_events"
$env:REDIS_ADDR="127.0.0.1:6379"
go run ./services/tasks/cmd/tasks
```

После запуска в логах `tasks` должны появиться строки о подключении к PostgreSQL, Redis и RabbitMQ.

Пример:

```text
connected to database
connected to redis
connected to rabbitmq
```

### Получение токена

Для проверки удобно использовать Postman.

Параметры запроса:

- метод: `POST`
- URL: `http://localhost:8081/v1/auth/login`
- заголовок: `Content-Type: application/json`
- тело:

```json
{
  "username": "student",
  "password": "student"
}
```

Ожидаемый ответ:

```json
{
  "access_token": "demo-token",
  "token_type": "Bearer"
}
```

### Создание задачи через REST API

Для проверки также можно использовать Postman.

Параметры запроса:

- метод: `POST`
- URL: `http://localhost:8082/v1/tasks`
- заголовки:
  - `Authorization: Bearer demo-token`
  - `Content-Type: application/json`
  - `X-Request-ID: pz13-001`
- тело:

```json
{
  "title": "RabbitMQ demo",
  "description": "publish event"
}
```

Ожидаемый результат:

- сервис `tasks` возвращает `201 Created`;
- задача сохраняется в PostgreSQL;
- событие `task.created` публикуется в очередь `task_events`;
- сервис `worker` получает сообщение и фиксирует его в логах.

<img width="1063" height="736" alt="image" src="https://github.com/user-attachments/assets/9fdc9464-a6e0-40db-af3d-06950e5d6f72" /> 

### Логи worker

В терминале `worker` должно появиться сообщение о полученном событии.

Пример:

```text
task event received event=task.created task_id=t_a1b2c3d4 ts=2026-05-18T12:00:00Z request_id=pz13-001 producer=tasks version=v1
```

<img width="1713" height="95" alt="image" src="https://github.com/user-attachments/assets/018c9496-6ed6-40e3-9fb9-bba11f710aa2" /> 


### Проверка через RabbitMQ Management UI

Открыть [http://localhost:15672](http://localhost:15672), войти под `guest / guest`, затем перейти в раздел `Queues and Streams` и открыть очередь `task_events`.

На странице должно быть видно:

- имя очереди;
- наличие consumer;
- количество сообщений в очереди;
- статистику обработки.

Место для скриншота RabbitMQ UI:

<img width="1633" height="654" alt="image" src="https://github.com/user-attachments/assets/80e9058c-8a05-464a-b55f-eb16372c5d84" />


---

## Выводы

- RabbitMQ успешно интегрирован в существующий проект.
- Очередь `task_events` объявляется как `durable`.
- Сервис `tasks` публикует событие `task.created` после успешного создания задачи.
- Отдельный сервис `worker` получает сообщения из очереди с ручным подтверждением обработки.
- Использование `prefetch = 1` ограничивает число неподтвержденных сообщений и демонстрирует последовательную обработку.
- Реализован полный учебный сценарий прохождения сообщения от HTTP-запроса до обработки в consumer.
