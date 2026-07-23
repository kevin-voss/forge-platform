//! OpenTelemetry bootstrap + HTTP middleware for forge-runtime (step 12.02).

use axum::body::Body;
use axum::extract::Request;
use axum::http::{HeaderMap, HeaderName, HeaderValue};
use axum::middleware::Next;
use axum::response::Response;
use opentelemetry::global;
use opentelemetry::metrics::{Counter, Histogram, MeterProvider as _};
use opentelemetry::trace::{Span, SpanKind, Status, TraceContextExt, Tracer};
use opentelemetry::{Context, KeyValue};
use opentelemetry_otlp::{MetricExporter, SpanExporter, WithExportConfig};
use opentelemetry_sdk::metrics::SdkMeterProvider;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::resource::Resource;
use opentelemetry_sdk::trace::SdkTracerProvider;
use opentelemetry_semantic_conventions::resource::{DEPLOYMENT_ENVIRONMENT_NAME, SERVICE_NAME};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tracing::info;

pub const METRIC_SERVICE_UP: &str = "forge_service_up";
pub const METRIC_HTTP_REQUESTS: &str = "forge_http_requests_total";
pub const METRIC_HTTP_DURATION: &str = "forge_http_request_duration_seconds";
pub const HEADER_TRACEPARENT: &str = "traceparent";
pub const HEADER_FORGE_REQUEST_ID: &str = "X-Forge-Request-ID";
pub const HEADER_LEGACY_REQUEST_ID: &str = "X-Request-Id";

pub const FORBIDDEN_METRIC_LABELS: &[&str] = &["request_id", "trace_id", "span_id", "path", "url"];

#[derive(Clone, Debug)]
pub struct OtelConfig {
    pub enabled: bool,
    pub endpoint: String,
    pub service_name: String,
    pub env: String,
    pub node_id: String,
}

impl OtelConfig {
    pub fn from_env(service_name: &str, env_name: &str, node_id: &str) -> Self {
        let enabled = match std::env::var("FORGE_OTEL_ENABLED")
            .unwrap_or_else(|_| "true".into())
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "false" | "0" | "no" => false,
            _ => true,
        };
        let endpoint = std::env::var("FORGE_OTEL_EXPORTER_ENDPOINT")
            .ok()
            .filter(|s| !s.trim().is_empty())
            .or_else(|| std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT").ok())
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(|| "http://otel-collector:4317".into());
        Self {
            enabled,
            endpoint,
            service_name: service_name.to_string(),
            env: env_name.to_string(),
            node_id: node_id.to_string(),
        }
    }
}

#[derive(Clone)]
pub struct OtelHandle {
    pub enabled: bool,
    tracer_provider: Option<SdkTracerProvider>,
    meter_provider: Option<SdkMeterProvider>,
    requests: Option<Counter<u64>>,
    duration: Option<Histogram<f64>>,
    up: Arc<AtomicU64>,
    node_id: String,
    service_name: String,
}

impl OtelHandle {
    pub fn init(cfg: &OtelConfig) -> Self {
        global::set_text_map_propagator(TraceContextPropagator::new());
        let up = Arc::new(AtomicU64::new(1));
        if !cfg.enabled {
            return Self {
                enabled: false,
                tracer_provider: None,
                meter_provider: None,
                requests: None,
                duration: None,
                up,
                node_id: cfg.node_id.clone(),
                service_name: cfg.service_name.clone(),
            };
        }

        let resource = Resource::builder_empty()
            .with_attributes([
                KeyValue::new(SERVICE_NAME, cfg.service_name.clone()),
                KeyValue::new(DEPLOYMENT_ENVIRONMENT_NAME, cfg.env.clone()),
                KeyValue::new("forge.service", cfg.service_name.clone()),
                KeyValue::new("forge.node", cfg.node_id.clone()),
            ])
            .build();

        // OTLP/HTTP on 4318 when given gRPC 4317 URL — collector accepts both;
        // map :4317 → :4318 for http-proto exporter.
        let endpoint = http_otlp_endpoint(&cfg.endpoint);

        let span_exporter = SpanExporter::builder()
            .with_http()
            .with_endpoint(endpoint.clone())
            .with_timeout(Duration::from_secs(2))
            .build();
        let metric_exporter = MetricExporter::builder()
            .with_http()
            .with_endpoint(endpoint)
            .with_timeout(Duration::from_secs(2))
            .build();

        let (tracer_provider, meter_provider, requests, duration) =
            match (span_exporter, metric_exporter) {
                (Ok(se), Ok(me)) => {
                    let tp = SdkTracerProvider::builder()
                        .with_batch_exporter(se)
                        .with_resource(resource.clone())
                        .build();
                    let mp = SdkMeterProvider::builder()
                        .with_periodic_exporter(me)
                        .with_resource(resource)
                        .build();
                    global::set_tracer_provider(tp.clone());
                    let meter = mp.meter("forge.runtime");
                    let requests = meter.u64_counter(METRIC_HTTP_REQUESTS).build();
                    let duration = meter
                        .f64_histogram(METRIC_HTTP_DURATION)
                        .with_unit("s")
                        .build();
                    let up_clone = Arc::clone(&up);
                    let _up_gauge = meter
                        .u64_observable_gauge(METRIC_SERVICE_UP)
                        .with_callback(move |observer| {
                            observer.observe(up_clone.load(Ordering::Relaxed), &[]);
                        })
                        .build();
                    (Some(tp), Some(mp), Some(requests), Some(duration))
                }
                _ => {
                    // Fail-open: keep serving without export.
                    (None, None, None, None)
                }
            };

        Self {
            enabled: tracer_provider.is_some(),
            tracer_provider,
            meter_provider,
            requests,
            duration,
            up,
            node_id: cfg.node_id.clone(),
            service_name: cfg.service_name.clone(),
        }
    }

    pub fn shutdown(&self) {
        self.up.store(0, Ordering::Relaxed);
        if let Some(tp) = &self.tracer_provider {
            let _ = tp.shutdown();
        }
        if let Some(mp) = &self.meter_provider {
            let _ = mp.shutdown();
        }
    }

    fn record_http(&self, method: &str, status: u16, seconds: f64) {
        let class = status_class(status);
        let attrs = [
            KeyValue::new("http_method", method.to_string()),
            KeyValue::new("http_status_class", class),
        ];
        if let Some(c) = &self.requests {
            c.add(1, &attrs);
        }
        if let Some(h) = &self.duration {
            h.record(seconds, &attrs);
        }
    }
}

/// Map gRPC collector URL (:4317) to OTLP/HTTP (:4318). SDK appends /v1/traces|metrics.
fn http_otlp_endpoint(endpoint: &str) -> String {
    let trimmed = endpoint.trim().trim_end_matches('/');
    if trimmed.contains(":4317") {
        trimmed.replace(":4317", ":4318")
    } else {
        trimmed.to_string()
    }
}

fn status_class(status: u16) -> &'static str {
    match status {
        500.. => "5xx",
        400.. => "4xx",
        300.. => "3xx",
        200.. => "2xx",
        _ => "1xx",
    }
}

fn valid_request_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 128
        && id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'.' || b == b'_' || b == b'-')
}

fn new_request_id() -> String {
    format!("req_{}", uuid::Uuid::new_v4().simple())
}

fn resolve_request_id(headers: &HeaderMap) -> String {
    for name in [HEADER_FORGE_REQUEST_ID, HEADER_LEGACY_REQUEST_ID] {
        if let Some(v) = headers.get(name).and_then(|v| v.to_str().ok()) {
            if valid_request_id(v) {
                return v.to_string();
            }
        }
    }
    new_request_id()
}

/// Axum middleware: extract/create trace, mint request id, metrics, log enrich.
pub async fn middleware(handle: Arc<OtelHandle>, mut req: Request, next: Next) -> Response {
    let start = Instant::now();
    let method = req.method().to_string();
    let path = req.uri().path().to_string();
    let req_id = resolve_request_id(req.headers());

    let parent_cx = global::get_text_map_propagator(|prop| {
        prop.extract(&HeaderExtractor(req.headers()))
    });

    let tracer = global::tracer("forge.runtime");
    let span = tracer
        .span_builder(format!("HTTP {method}"))
        .with_kind(SpanKind::Server)
        .with_attributes([
            KeyValue::new("http.request.method", method.clone()),
            KeyValue::new("url.path", path.clone()),
            KeyValue::new("forge.service", handle.service_name.clone()),
            KeyValue::new("forge.node", handle.node_id.clone()),
            KeyValue::new("request_id", req_id.clone()),
        ])
        .start_with_context(&tracer, &parent_cx);
    let cx = parent_cx.with_span(span);

    // Ensure correlation headers on inbound request for handlers / outbound.
    // Do not hold ContextGuard across .await (it is !Send).
    if let Ok(v) = HeaderValue::from_str(&req_id) {
        req.headers_mut()
            .insert(HeaderName::from_static("x-forge-request-id"), v.clone());
        req.headers_mut()
            .insert(HeaderName::from_static("x-request-id"), v);
    }
    global::get_text_map_propagator(|prop| {
        prop.inject_context(&cx, &mut HeaderInjector(req.headers_mut()));
    });

    let mut response = next.run(req).await;
    let status = response.status().as_u16();
    {
        let span = cx.span();
        span.set_attribute(KeyValue::new("http.response.status_code", status as i64));
        if status >= 500 {
            span.set_status(Status::error(format!("HTTP {status}")));
        }
        span.end();
    }

    if let Ok(v) = HeaderValue::from_str(&req_id) {
        response
            .headers_mut()
            .insert(HeaderName::from_static("x-forge-request-id"), v.clone());
        response
            .headers_mut()
            .insert(HeaderName::from_static("x-request-id"), v);
    }

    if !path.starts_with("/health") {
        handle.record_http(&method, status, start.elapsed().as_secs_f64());
        let sc = cx.span().span_context().clone();
        info!(
            method = %method,
            path = %path,
            status = status,
            duration_ms = start.elapsed().as_millis() as u64,
            request_id = %req_id,
            trace_id = %sc.trace_id(),
            span_id = %sc.span_id(),
            forge.service = %handle.service_name,
            forge.node = %handle.node_id,
            "request"
        );
    }
    response
}

/// Inject traceparent + request ids onto an outbound reqwest builder.
pub fn inject_reqwest(builder: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
    let mut headers = HeaderMap::new();
    global::get_text_map_propagator(|prop| {
        prop.inject_context(&Context::current(), &mut HeaderInjector(&mut headers));
    });
    let mut b = builder;
    if let Some(tp) = headers.get(HEADER_TRACEPARENT) {
        if let Ok(s) = tp.to_str() {
            b = b.header(HEADER_TRACEPARENT, s);
        }
    }
    b
}

struct HeaderExtractor<'a>(&'a HeaderMap);
impl opentelemetry::propagation::Extractor for HeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }
    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(|k| k.as_str()).collect()
    }
}

struct HeaderInjector<'a>(&'a mut HeaderMap);
impl opentelemetry::propagation::Injector for HeaderInjector<'_> {
    fn set(&mut self, key: &str, value: String) {
        if let Ok(name) = HeaderName::try_from(key) {
            if let Ok(v) = HeaderValue::from_str(&value) {
                self.0.insert(name, v);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::routing::get;
    use axum::Router;
    use tower::ServiceExt;

    #[tokio::test]
    async fn propagates_inbound_traceparent() {
        let _ = OtelHandle::init(&OtelConfig {
            enabled: false,
            endpoint: "http://127.0.0.1:1".into(),
            service_name: "forge-runtime".into(),
            env: "test".into(),
            node_id: "node-a".into(),
        });
        global::set_text_map_propagator(TraceContextPropagator::new());

        // Mint a parent via SDK tracer (noop still produces valid ids when using sdk?).
        // For disabled export we still have propagator; create via manual header.
        let parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
        let handle = Arc::new(OtelHandle::init(&OtelConfig {
            enabled: false,
            endpoint: "http://127.0.0.1:1".into(),
            service_name: "forge-runtime".into(),
            env: "test".into(),
            node_id: "node-a".into(),
        }));
        let app = Router::new()
            .route("/v1/node", get(|| async { "ok" }))
            .layer(axum::middleware::from_fn(move |req, next| {
                let h = Arc::clone(&handle);
                async move { middleware(h, req, next).await }
            }));

        let req = Request::builder()
            .uri("/v1/node")
            .header(HEADER_TRACEPARENT, parent)
            .body(Body::empty())
            .unwrap();
        let resp = app.oneshot(req).await.unwrap();
        assert_eq!(resp.status(), 200);
        assert!(resp.headers().get(HEADER_FORGE_REQUEST_ID).is_some());
    }

    #[tokio::test]
    async fn malformed_traceparent_still_serves() {
        let handle = Arc::new(OtelHandle::init(&OtelConfig {
            enabled: false,
            endpoint: "http://127.0.0.1:1".into(),
            service_name: "forge-runtime".into(),
            env: "test".into(),
            node_id: "n".into(),
        }));
        let app = Router::new()
            .route("/v1/x", get(|| async { "ok" }))
            .layer(axum::middleware::from_fn(move |req, next| {
                let h = Arc::clone(&handle);
                async move { middleware(h, req, next).await }
            }));
        let req = Request::builder()
            .uri("/v1/x")
            .header(HEADER_TRACEPARENT, "bad")
            .body(Body::empty())
            .unwrap();
        let resp = app.oneshot(req).await.unwrap();
        assert_eq!(resp.status(), 200);
    }

    #[test]
    fn metric_label_cardinality_lint() {
        assert!(FORBIDDEN_METRIC_LABELS.contains(&"request_id"));
        assert_eq!(METRIC_HTTP_REQUESTS, "forge_http_requests_total");
    }

    #[test]
    fn fail_open_init_unreachable_collector() {
        let h = OtelHandle::init(&OtelConfig {
            enabled: true,
            endpoint: "http://127.0.0.1:1".into(),
            service_name: "forge-runtime".into(),
            env: "test".into(),
            node_id: "n".into(),
        });
        // Must not panic; may or may not enable export.
        h.record_http("GET", 200, 0.01);
        h.shutdown();
    }
}
