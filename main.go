package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/segmentio/kafka-go"
)

type jsonErr struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v", rec)
				writeJSON(w, http.StatusInternalServerError, jsonErr{"internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func kafkaWriteHandler(writer *kafka.Writer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msg := r.URL.Query().Get("message")
		if msg == "" {
			writeJSON(w, http.StatusBadRequest, jsonErr{"missing 'message' query param"})
			return
		}

		// Таймаут на запись в Kafka
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		err := writer.WriteMessages(ctx,
			kafka.Message{
				// ключ опционален, но полезен для партиционирования
				Key:   []byte("health"),
				Value: []byte(msg),
			},
		)
		if err != nil {
			log.Printf("kafka write error: %v", err)
			writeJSON(w, http.StatusBadGateway, jsonErr{"kafka write failed"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "kafka 📝"})
	}
}

func main() {
	// Адрес брокера и топик можно вытащить из переменных окружения
	brokers := []string{getEnv("KAFKA_BROKER", "localhost:9092")}
	topic := getEnv("KAFKA_TOPIC", "health-events")

	writer := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		RequiredAcks: kafka.RequireOne,
		Balancer:     &kafka.LeastBytes{},
		Async:        false, // чтобы получать ошибки синхронно
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health_check", healthCheckHandler)
	mux.Handle("/kafka_write", kafkaWriteHandler(writer))

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      recoverMiddleware(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Грациозное завершение
	idleConnsClosed := make(chan struct{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		log.Println("shutting down...")

		_ = srv.Shutdown(context.Background())
		_ = writer.Close()
		close(idleConnsClosed)
	}()

	log.Println("Server is running on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	<-idleConnsClosed
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
