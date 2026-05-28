package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"pz1.2/services/tasks/graph"
	tasksamqp "pz1.2/services/tasks/internal/amqp"
	"pz1.2/services/tasks/internal/cache"
	"pz1.2/services/tasks/internal/client/authclient"
	taskshttp "pz1.2/services/tasks/internal/http"
	"pz1.2/services/tasks/internal/repository"
	"pz1.2/services/tasks/internal/service"
	"pz1.2/shared/logger"
	"pz1.2/shared/middleware"
)

func main() {
	log := logger.New("tasks")
	defer log.Sync()

	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		instanceID = "tasks-default"
	}
	log.Info("instance identity", zap.String("instance_id", instanceID))

	port := os.Getenv("TASKS_PORT")
	if port == "" {
		port = "8082"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://tasks:tasks@localhost:5432/tasks?sslmode=disable"
	}

	repo, err := repository.NewPostgresRepository(dsn)
	if err != nil {
		log.Fatal("failed to connect to database", zap.Error(err))
	}
	defer repo.Close()
	log.Info("connected to database")

	authMode := os.Getenv("AUTH_MODE")
	if authMode == "" {
		authMode = "http"
	}

	var authVerifier authclient.AuthVerifier

	switch authMode {
	case "grpc":
		grpcAddr := os.Getenv("AUTH_GRPC_ADDR")
		if grpcAddr == "" {
			grpcAddr = "localhost:50051"
		}
		log.Info("using gRPC auth client", zap.String("addr", grpcAddr))
		client, err := authclient.NewGRPCClient(grpcAddr, 2*time.Second, log)
		if err != nil {
			log.Fatal("failed to create gRPC auth client", zap.Error(err))
		}
		authVerifier = client
		defer client.Close()
	default:
		authBaseURL := os.Getenv("AUTH_BASE_URL")
		if authBaseURL == "" {
			authBaseURL = "http://localhost:8081"
		}
		log.Info("using HTTP auth client", zap.String("url", authBaseURL))
		authVerifier = authclient.NewHTTPClient(authBaseURL, 3*time.Second, log)
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6379"
	}

	cacheTTL := 120 * time.Second
	if v := os.Getenv("CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cacheTTL = d
		}
	}

	cacheTTLJitter := 30 * time.Second
	if v := os.Getenv("CACHE_TTL_JITTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cacheTTLJitter = d
		}
	}

	var redisClient *redis.Client
	redisClient = cache.NewRedisClient(redisAddr, "", 2*time.Second, 2*time.Second, 2*time.Second)
	if err := cache.Ping(context.Background(), redisClient); err != nil {
		log.Warn("redis unavailable at startup, caching disabled", zap.Error(err))
		redisClient = nil
	} else {
		log.Info("connected to redis", zap.String("addr", redisAddr))
	}

	rabbitURL := os.Getenv("RABBIT_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}

	queueName := os.Getenv("QUEUE_NAME")
	if queueName == "" {
		queueName = "task_events"
	}

	publisher := tasksamqp.NewPublisher(rabbitURL, queueName, "tasks", "v1", log)
	defer publisher.Close()

	taskService := service.NewTaskService(repo, redisClient, log, cacheTTL, cacheTTLJitter, publisher)

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(taskService, authVerifier, log, instanceID)
	handler.RegisterRoutes(mux)
	mux.Handle("GET /metrics", promhttp.Handler())

	gqlSrv := gqlhandler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{
		Resolvers: &graph.Resolver{TaskService: taskService},
	}))
	mux.Handle("POST /query", taskshttp.AuthMiddlewareFunc(authVerifier, log, gqlSrv))
	mux.Handle("GET /", playground.Handler("GraphQL Playground", "/query"))
	log.Info("GraphQL playground available at /")

	core := taskshttp.InstanceIDMiddleware(instanceID, mux)
	httpHandler := middleware.Metrics(middleware.RequestID(middleware.AccessLog(log, zap.String("instance_id", instanceID))(core)))

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      httpHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("HTTP server starting", zap.String("port", port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("HTTP server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("server shutdown failed", zap.Error(err))
	}

	log.Info("server stopped")
}
