package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/riandyrn/otelchi"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer                   trace.Tracer
	meter                    metric.Meter
	requestCounter           metric.Int64Counter
	itemsCounter             metric.Int64UpDownCounter
	fanSpeedSubsciption      chan int64
	speedGauge               metric.Int64Gauge
	histogram                metric.Float64Histogram
	memoryObservable         metric.Float64ObservableCounter
	currentMemoryUsage       float64
	connectionObservable     metric.Int64ObservableUpDownCounter
	activeConnections        int64
	activeConnectionsMutex   sync.Mutex
)

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

func newOTelMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	// Get OTLP endpoint from environment variable
	endpoint := os.Getenv("OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("OTLP_ENDPOINT environment variable is required")
	}

	log.Printf("Initializing OpenTelemetry Metrics with OTLP endpoint: %s", endpoint)

	// Create OTLP metric exporter
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	return exporter, nil
}

// Create resource with service information
func newResource() (*resource.Resource, error) {
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("go-app"),
		),
	)
}

func newTracerProvider(exp sdktrace.SpanExporter, res *resource.Resource) *sdktrace.TracerProvider {
	// Create TracerProvider
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
}

func newMeterProvider(metricExporter sdkmetric.Exporter, res *resource.Resource) *sdkmetric.MeterProvider {
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExporter,
				// デモ目的で3sに設定（デフォルトは1m）
				sdkmetric.WithInterval(3*time.Second)),
		),
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

	// メトリクスをカウント
	if requestCounter != nil {
		requestCounter.Add(r.Context(), 1, metric.WithAttributes(
			attribute.String("endpoint", "/hello"),
			attribute.String("method", r.Method),
		))
		log.Printf("Incremented request counter for /hello endpoint")
	}

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
	// span の作成。作成次に属性を設定可能
	_, span := tracer.Start(r.Context(), "getError")
	defer span.End()

	// ステータスにエラーを設定。設定すると、このスパンだけでなく、トレース全体がエラーとして扱われる
	span.SetStatus(codes.Error, "Internal Server Error")
	// Event にエラー情報を追加する。このメソッドだけでは、トレース全体のステータスは変わらない（厳密には、直前のスパンまでエラーステータスになる）ため、span.SetStatus と合わせて使う
	span.RecordError(fmt.Errorf("err: Internal Server Error"))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	data, _ := json.Marshal(map[string]string{
		"error":   "Internal Server Error",
		"message": "This is a sample 500 error endpoint for testing OpenTelemetry",
	})
	w.Write(data)
}

func addItem(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "addItem")
	defer span.End()

	// itemsCounterをインクリメント
	if itemsCounter != nil {
		itemsCounter.Add(ctx, 1)
		log.Printf("Incremented items counter")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]string{
		"message": "Item added successfully",
		"action":  "increment",
	})
	w.Write(data)
}

func removeItem(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "removeItem")
	defer span.End()

	// itemsCounterをデクリメント
	if itemsCounter != nil {
		itemsCounter.Add(ctx, -1)
		log.Printf("Decremented items counter")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]string{
		"message": "Item removed successfully",
		"action":  "decrement",
	})
	w.Write(data)
}

func getCPUFanSpeedHandler(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "getCPUFanSpeed")
	defer span.End()

	// fanSpeedSubsciptionから最新の値を非ブロッキングで取得
	var fanSpeed int64
	select {
	case speed, ok := <-fanSpeedSubsciption:
		if ok {
			fanSpeed = speed
			// Gaugeメトリクスを記録
			if speedGauge != nil {
				speedGauge.Record(ctx, fanSpeed)
				log.Printf("Recorded fan speed: %d rpm", fanSpeed)
			}
		} else {
			// チャンネルがクローズされている場合はランダムな値を生成
			fanSpeed = int64(1500 + rand.Intn(1000))
			if speedGauge != nil {
				speedGauge.Record(ctx, fanSpeed)
			}
		}
	default:
		// チャンネルに値がない場合はランダムな値を生成
		fanSpeed = int64(1500 + rand.Intn(1000))
		if speedGauge != nil {
			speedGauge.Record(ctx, fanSpeed)
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"fanSpeed": fanSpeed,
		"unit":     "rpm",
		"message":  "Current CPU fan speed",
	})
	w.Write(data)
}

func callExternalAPI(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "callExternalAPI")
	defer span.End()

	// 処理開始時刻を記録
	startTime := time.Now()
	
	// 外部APIコールをエミュレート（50ms～10秒のランダムな遅延、より分散させる）
	// 50% : 50ms - 1秒 (高速レスポンス)
	// 30% : 1秒 - 5秒 (中程度のレスポンス)
	// 20% : 5秒 - 10秒 (遅いレスポンス)
	var apiLatency time.Duration
	randValue := rand.Float32()
	if randValue < 0.5 {
		// 50ms - 1000ms
		apiLatency = time.Duration(50+rand.Intn(950)) * time.Millisecond
	} else if randValue < 0.8 {
		// 1秒 - 5秒
		apiLatency = time.Duration(1000+rand.Intn(4000)) * time.Millisecond
	} else {
		// 5秒 - 10秒
		apiLatency = time.Duration(5000+rand.Intn(5000)) * time.Millisecond
	}
	
	// スパンに属性を追加
	span.SetAttributes(
		attribute.String("api.endpoint", "https://api.example.com/data"),
		attribute.String("api.method", "GET"),
		attribute.Int64("api.latency_ms", int64(apiLatency.Milliseconds())),
	)
	
	// 外部APIコールの開始をイベントとして記録
	span.AddEvent("External API call started", trace.WithAttributes(
		attribute.String("api.url", "https://api.example.com/data"),
	))
	
	// 外部APIコールをエミュレート
	time.Sleep(apiLatency)
	
	// 外部APIコールの完了をイベントとして記録
	span.AddEvent("External API call completed", trace.WithAttributes(
		attribute.Int("api.status_code", 200),
	))
	
	// 処理時間を計測
	duration := time.Since(startTime).Seconds()
	
	// ヒストグラムメトリクスに記録
	if histogram != nil {
		histogram.Record(ctx, duration, metric.WithAttributes(
			attribute.String("api.endpoint", "external_api"),
			attribute.Int("api.status_code", 200),
		))
		log.Printf("Recorded API call duration: %.3fs", duration)
	}
	
	// レスポンスを返す
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"message":     "External API call completed successfully",
		"duration_ms": apiLatency.Milliseconds(),
		"status":      "success",
	})
	w.Write(data)
}

func getMemoryMetrics(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "getMemoryMetrics")
	defer span.End()

	// 現在のメモリ使用量を返す（ObservableCounterによって自動的に収集されている値）
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"current_memory_bytes": currentMemoryUsage,
		"unit":                 "bytes",
		"message":              "Current memory usage tracked by Observable Counter",
	})
	w.Write(data)
}

func getConnectionMetrics(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "getConnectionMetrics")
	defer span.End()

	// 現在のアクティブコネクション数を返す
	activeConnectionsMutex.Lock()
	connections := activeConnections
	activeConnectionsMutex.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"active_connections": connections,
		"unit":               "connections",
		"message":            "Active connections tracked by Observable UpDownCounter",
	})
	w.Write(data)
}

func simulateConnect(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "simulateConnect")
	defer span.End()

	// コネクションを増やす
	activeConnectionsMutex.Lock()
	activeConnections++
	connections := activeConnections
	activeConnectionsMutex.Unlock()

	log.Printf("Connection opened, total: %d", connections)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"action":             "connect",
		"active_connections": connections,
		"message":            "Connection opened",
	})
	w.Write(data)
}

func simulateDisconnect(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "simulateDisconnect")
	defer span.End()

	// コネクションを減らす
	activeConnectionsMutex.Lock()
	if activeConnections > 0 {
		activeConnections--
	}
	connections := activeConnections
	activeConnectionsMutex.Unlock()

	log.Printf("Connection closed, total: %d", connections)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(map[string]interface{}{
		"action":             "disconnect",
		"active_connections": connections,
		"message":            "Connection closed",
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

	metricExp, err := newOTelMetricExporter(ctx)
	if err != nil {
		log.Fatalf("failed to create metric exporter: %v", err)
	}

	res, err := newResource()
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	tp := newTracerProvider(exp, res)

	defer func() { _ = tp.Shutdown(ctx) }()

	otel.SetTracerProvider(tp)
	// 伝搬を設定。nginx や他サービスとのトレースIDの受け渡しに利用できる
	otel.SetTextMapPropagator(propagation.TraceContext{})
	mp := newMeterProvider(metricExp, res)
	defer func() {
		log.Printf("Shutting down meter provider...")
		if err := mp.Shutdown(ctx); err != nil {
			log.Fatalf("failed to shutdown meter provider: %v", err)
		}
		log.Printf("Meter provider shutdown complete")
	}()
	otel.SetMeterProvider(mp)

	tracer = tp.Tracer("go-app")

	// メトリクスカウンターを作成
	meter = otel.Meter("go-app")
	requestCounter, err = meter.Int64Counter(
		"api.counter",
		metric.WithDescription("Number of API calls"),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		log.Fatalf("failed to create request counter: %v", err)
	}
	log.Printf("Request counter created successfully")

	itemsCounter, err = meter.Int64UpDownCounter(
		"items.counter",
		metric.WithDescription("Number of items."),
		metric.WithUnit("{item}"),
	)
	if err != nil {
		log.Fatalf("failed to create items counter: %v", err)
	}

	speedGauge, err = meter.Int64Gauge(
		"cpu.fan.speed",
		metric.WithDescription("CPU Fan Speed"),
		metric.WithUnit("{rpm}"),
	)
	if err != nil {
		log.Fatalf("failed to create speed gauge: %v", err)
	}

	getCPUFanSpeed := func() int64 {
		// デモンストレーション目的でランダムなファン速度を生成します
		// 実際のアプリケーションでは、これを実際のファン速度を取得するように置き換えてください
		return int64(1500 + rand.Intn(1000))
	}

	fanSpeedSubsciption = make(chan int64, 1)
	go func() {
		defer close(fanSpeedSubsciption)

		for idx := 0; idx < 5; idx++ {
			time.Sleep(time.Duration(rand.Intn(3)) * time.Second)
			fanSpeed := getCPUFanSpeed()
			fanSpeedSubsciption <- fanSpeed
		}
	}()

	histogram, err = meter.Float64Histogram(
		"task.duration",
		metric.WithDescription("The duration of task execution."),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	// Float64ObservableCounterを作成
	memoryObservable, err = meter.Float64ObservableCounter(
		"memory.usage",
		metric.WithDescription("Current memory usage in bytes"),
		metric.WithUnit("By"),
		metric.WithFloat64Callback(func(ctx context.Context, o metric.Float64Observer) error {
			// デモンストレーション目的でランダムな値を生成
			// 実際のアプリケーションでは、実際のメモリ使用量を取得するように置き換えてください
			// 100MB〜500MBの間でランダムに増加する値をシミュレート
			currentMemoryUsage = float64(100*1024*1024) + float64(rand.Intn(400*1024*1024))
			o.Observe(currentMemoryUsage, metric.WithAttributes(
				attribute.String("memory.type", "heap"),
			))
			log.Printf("Observable Counter reported memory usage: %.2f MB", currentMemoryUsage/(1024*1024))
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("failed to create memory observable counter: %v", err)
	}
	log.Printf("Memory observable counter created successfully")

	// Int64ObservableUpDownCounterを作成
	connectionObservable, err = meter.Int64ObservableUpDownCounter(
		"active.connections",
		metric.WithDescription("Number of active connections"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			activeConnectionsMutex.Lock()
			connections := activeConnections
			activeConnectionsMutex.Unlock()
			
			o.Observe(connections, metric.WithAttributes(
				attribute.String("connection.type", "http"),
			))
			log.Printf("Observable UpDownCounter reported active connections: %d", connections)
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("failed to create connection observable updown counter: %v", err)
	}
	log.Printf("Connection observable updown counter created successfully")

	// バックグラウンドでコネクション数をシミュレート
	go func() {
		for {
			time.Sleep(time.Duration(2+rand.Intn(3)) * time.Second)
			activeConnectionsMutex.Lock()
			// -5〜+10の間でランダムに変動
			change := int64(rand.Intn(16) - 5)
			activeConnections += change
			if activeConnections < 0 {
				activeConnections = 0
			}
			if activeConnections > 100 {
				activeConnections = 100
			}
			activeConnectionsMutex.Unlock()
			log.Printf("Simulated connection change: %+d, total: %d", change, activeConnections)
		}
	}()

	// Create chi router
	r := chi.NewRouter()

	r.Use(otelchi.Middleware("go-app"))

	// Define routes
	r.Get("/healthz", getHealtz)
	r.Get("/", getRoot)
	r.Get("/hello", getHello)
	r.Get("/users/{id}", getUserByID)
	r.Get("/error", getError)
	r.Post("/items/add", addItem)
	r.Post("/items/remove", removeItem)
	r.Get("/cpu/fanspeed", getCPUFanSpeedHandler)
	r.Get("/external-api", callExternalAPI)
	r.Get("/metrics/memory", getMemoryMetrics)
	r.Get("/metrics/connections", getConnectionMetrics)
	r.Post("/connection/open", simulateConnect)
	r.Post("/connection/close", simulateDisconnect)

	log.Fatal(http.ListenAndServe(":8080", r))
}
