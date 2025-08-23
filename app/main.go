package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/riandyrn/otelchi"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer

func newOTelTUIExporter(ctx context.Context) (*otlptrace.Exporter, error) {
	// Get New OTel TUI endpoint from environment variable or use default
	endpoint := os.Getenv("OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("OTLP_ENDPOINT environment variable is required")
	}

	log.Printf("Initializing OpenTelemetry with OTLP endpoint: %s", endpoint)

	// Create OTLP trace exporter with New Relic configuration
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	return exporter, nil
}

func newTracerProvider(exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	// Create resource with service information
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("go-app"),
		),
	)
	if err != nil {
		panic(err)
	}

	// Create TracerProvider
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
}

func getHealtz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]string{"status": "ok"})
	w.Write(data)
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Welcome to the chi HTTP server behind Nginx!\n"))
}

func getHello(w http.ResponseWriter, r *http.Request) {
	// span の作成。作成次に属性を設定可能
	ctx, span := tracer.Start(r.Context(), "getHello", trace.WithAttributes(attribute.String("hello", "world")))
	// さらに属性を追加することも可能
	span.SetAttributes(attribute.Bool("isTrue", true), attribute.String("stringAttr", "hi!"))
	// 属性のキーは、事前に定義されたものも利用できる
	myKey := attribute.Key("myCoolAttribute")
	span.SetAttributes(myKey.String("a value"))
	defer span.End()

	// AddEvent により特定のタイミングで、Event を追加可能。mutex で排他処理をしているときや、特定の分岐に入る時などに利用できそう
	span.AddEvent("Hello with AddEvent")

	childHello(ctx)

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "World"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]string{"message": fmt.Sprintf("Hello, %s!", name)})
	w.Write(data)
}

func childHello(ctx context.Context) {
	_, childSpan := tracer.Start(ctx, "childHello")
	defer childSpan.End()

	childSpan.AddEvent("Hello with AddEvent from child", trace.WithAttributes(attribute.String("childEvent", "hello child")))
	fmt.Println("This is a child function")
}

func getUserByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]any{"id": id, "profile": map[string]any{"nickname": "guest", "created_at": time.Now().UTC()}})
	w.Write(data)
}

func getError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	data, _ := json.Marshal(map[string]string{
		"error":   "Internal Server Error",
		"message": "This is a sample 500 error endpoint for testing OpenTelemetry",
	})
	w.Write(data)
}

func main() {
	// Initialize OpenTelemetry
	ctx := context.Background()

	exp, err := newOTelTUIExporter(ctx)
	if err != nil {
		log.Fatalf("failed to create exporter: %v", err)
	}

	tp := newTracerProvider(exp)

	defer func() { _ = tp.Shutdown(ctx) }()

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer = tp.Tracer("go-app")

	// Create chi router
	r := chi.NewRouter()

	r.Use(otelchi.Middleware("go-app"))

	// Define routes
	r.Get("/healthz", getHealtz)
	r.Get("/", getRoot)
	r.Get("/hello", getHello)
	r.Get("/users/{id}", getUserByID)
	r.Get("/error", getError)

	log.Fatal(http.ListenAndServe(":8080", r))
}
