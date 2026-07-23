//! Streamed object upload / download / HEAD with SHA-256 + Range (13.03 / 13.04).

use crate::api::sign::post_sign;
use crate::api::validate::{validate_bucket_name, validate_object_key};
use crate::backend::BackendError;
use crate::config::VerifyOnRead;
use crate::http::range::{parse_bytes_range, unsatisfiable_content_range, RangeError};
use crate::meta::{MetaError, ObjectMeta};
use crate::project::ProjectContext;
use crate::state::AppState;
use axum::body::Body;
use axum::extract::{Extension, Path, State};
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::{Json, Router};
use bytes::Bytes;
use futures_util::StreamExt;
use std::io;
use std::sync::atomic::Ordering;
use std::time::Instant;
use tokio::io::AsyncReadExt;
use tokio_util::io::ReaderStream;
use tracing::{info, warn};

#[derive(Debug, serde::Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new().route(
        "/v1/buckets/{bucket}/objects/{*key}",
        axum::routing::put(put_object)
            .get(get_object)
            .head(head_object)
            .post(post_sign),
    )
}

fn request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-forge-request-id")
        .or_else(|| headers.get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string()
}

fn err_json(status: StatusCode, error: impl Into<String>, code: &'static str) -> Response {
    (
        status,
        Json(ErrorBody {
            error: error.into(),
            code: Some(code),
        }),
    )
        .into_response()
}

fn meta_err(err: MetaError) -> Response {
    match err {
        MetaError::NotFound => err_json(StatusCode::NOT_FOUND, "object not found", "not_found"),
        MetaError::Conflict(msg) => err_json(StatusCode::CONFLICT, msg, "conflict"),
        MetaError::Invalid(msg) => err_json(StatusCode::BAD_REQUEST, msg, "invalid"),
        MetaError::Internal(msg) => {
            warn!(error = %msg, "metadata store error");
            err_json(
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal error",
                "internal",
            )
        }
    }
}

fn backend_err(err: BackendError) -> Response {
    match err {
        BackendError::NotFound(_) => {
            err_json(StatusCode::NOT_FOUND, "object not found", "not_found")
        }
        BackendError::TooLarge { max_bytes } => err_json(
            StatusCode::PAYLOAD_TOO_LARGE,
            format!("object exceeds max size of {max_bytes} bytes"),
            "too_large",
        ),
        BackendError::ChecksumMismatch { expected, actual } => err_json(
            StatusCode::UNPROCESSABLE_ENTITY,
            format!("checksum mismatch: expected {expected}, got {actual}"),
            "checksum_mismatch",
        ),
        BackendError::Integrity(msg) => {
            warn!(error = %msg, "storage integrity error");
            err_json(
                StatusCode::INTERNAL_SERVER_ERROR,
                "object integrity check failed",
                "integrity_error",
            )
        }
        BackendError::Io(msg) => {
            warn!(error = %msg, "object I/O error");
            err_json(
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal error",
                "internal",
            )
        }
        BackendError::Fatal(msg) | BackendError::Unavailable(msg) => {
            warn!(error = %msg, "storage backend error");
            err_json(
                StatusCode::SERVICE_UNAVAILABLE,
                "storage unavailable",
                "unavailable",
            )
        }
    }
}

fn unavailable_meta() -> Response {
    err_json(
        StatusCode::SERVICE_UNAVAILABLE,
        "metadata store unavailable",
        "unavailable",
    )
}

fn normalize_key(raw: &str) -> Result<String, Response> {
    // Axum catch-all may include a leading slash depending on matching.
    let key = raw.trim_start_matches('/');
    if let Err(msg) = validate_object_key(key) {
        let _ = msg;
        return Err(err_json(
            StatusCode::BAD_REQUEST,
            "invalid object key",
            "invalid_key",
        ));
    }
    Ok(key.to_string())
}

/// Bridge an HTTP body stream into `AsyncRead` with fixed-size buffering at the FS layer.
struct BodyReader {
    stream: axum::body::BodyDataStream,
    pending: Option<Bytes>,
    offset: usize,
    failed: bool,
}

impl BodyReader {
    fn new(body: Body) -> Self {
        Self {
            stream: body.into_data_stream(),
            pending: None,
            offset: 0,
            failed: false,
        }
    }
}

impl tokio::io::AsyncRead for BodyReader {
    fn poll_read(
        mut self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
        buf: &mut tokio::io::ReadBuf<'_>,
    ) -> std::task::Poll<io::Result<()>> {
        use std::task::Poll;
        if self.failed {
            return Poll::Ready(Err(io::Error::new(
                io::ErrorKind::BrokenPipe,
                "upload stream already failed",
            )));
        }
        loop {
            if self.pending.is_some() {
                let pending = self.pending.take().unwrap();
                let rest = &pending[self.offset..];
                let n = rest.len().min(buf.remaining());
                buf.put_slice(&rest[..n]);
                self.offset += n;
                if self.offset < pending.len() {
                    self.pending = Some(pending);
                } else {
                    self.offset = 0;
                }
                return Poll::Ready(Ok(()));
            }
            match self.stream.poll_next_unpin(cx) {
                Poll::Ready(Some(Ok(chunk))) => {
                    if chunk.is_empty() {
                        continue;
                    }
                    self.pending = Some(chunk);
                    self.offset = 0;
                }
                Poll::Ready(Some(Err(err))) => {
                    self.failed = true;
                    return Poll::Ready(Err(io::Error::new(io::ErrorKind::BrokenPipe, err)));
                }
                Poll::Ready(None) => return Poll::Ready(Ok(())),
                Poll::Pending => return Poll::Pending,
            }
        }
    }
}

async fn put_object(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path((bucket, raw_key)): Path<(String, String)>,
    headers: HeaderMap,
    body: Body,
) -> Response {
    let started = Instant::now();
    let rid = request_id(&headers);

    if validate_bucket_name(&bucket).is_err() {
        return err_json(StatusCode::NOT_FOUND, "bucket not found", "not_found");
    }
    let key = match normalize_key(&raw_key) {
        Ok(k) => k,
        Err(resp) => return resp,
    };

    let Some(meta) = state.meta.as_ref() else {
        return unavailable_meta();
    };

    match meta.get_bucket(&project.project_id, &bucket) {
        Ok(_) => {}
        Err(MetaError::NotFound) => {
            return err_json(StatusCode::NOT_FOUND, "bucket not found", "not_found");
        }
        Err(err) => return meta_err(err),
    }

    let content_type = headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .unwrap_or("application/octet-stream")
        .to_string();

    let expected_sha = headers
        .get("x-expected-sha256")
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|s| !s.is_empty());

    let mut reader = BodyReader::new(body);
    let put = match state
        .backend
        .put_stream_hashed(
            &mut reader,
            state.stream_buffer_bytes,
            state.max_object_bytes,
            expected_sha,
        )
        .await
    {
        Ok(v) => v,
        Err(err) => {
            if matches!(err, BackendError::Integrity(_)) {
                state
                    .metrics
                    .storage_integrity_errors_total
                    .fetch_add(1, Ordering::Relaxed);
            }
            return backend_err(err);
        }
    };

    let (object, created) = match meta.upsert_object(
        &project.project_id,
        &bucket,
        &key,
        put.size_bytes as i64,
        Some(&content_type),
        &put.storage_path,
        &put.sha256,
    ) {
        Ok(v) => v,
        Err(err) => return meta_err(err),
    };

    state
        .metrics
        .storage_upload_bytes_total
        .fetch_add(put.size_bytes, Ordering::Relaxed);
    state
        .metrics
        .storage_uploads_total
        .fetch_add(1, Ordering::Relaxed);
    state
        .metrics
        .storage_objects_total
        .fetch_add(if created { 1 } else { 0 }, Ordering::Relaxed);
    if put.dedup_hit {
        state
            .metrics
            .storage_dedup_hits_total
            .fetch_add(1, Ordering::Relaxed);
    }

    let duration_ms = started.elapsed().as_millis() as u64;
    info!(
        project_id = %project.project_id,
        bucket = %bucket,
        key = %key,
        size_bytes = put.size_bytes,
        sha256 = %put.sha256,
        dedup_hit = put.dedup_hit,
        duration_ms,
        request_id = %rid,
        created,
        "object uploaded"
    );

    let status = if created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    (status, Json(object)).into_response()
}

async fn get_object(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path((bucket, raw_key)): Path<(String, String)>,
    headers: HeaderMap,
) -> Response {
    let started = Instant::now();
    let rid = request_id(&headers);
    match load_object_meta(&state, &project.project_id, &bucket, &raw_key) {
        Ok(object) => stream_download(state, object, headers, rid, started, true).await,
        Err(resp) => resp,
    }
}

async fn head_object(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path((bucket, raw_key)): Path<(String, String)>,
    headers: HeaderMap,
) -> Response {
    match load_object_meta(&state, &project.project_id, &bucket, &raw_key) {
        Ok(object) => {
            let started = Instant::now();
            stream_download(state, object, headers, "-".into(), started, false).await
        }
        Err(resp) => resp,
    }
}

fn load_object_meta(
    state: &AppState,
    project_id: &str,
    bucket: &str,
    raw_key: &str,
) -> Result<ObjectMeta, Response> {
    if validate_bucket_name(bucket).is_err() {
        return Err(err_json(
            StatusCode::NOT_FOUND,
            "object not found",
            "not_found",
        ));
    }
    let key = normalize_key(raw_key)?;
    let Some(meta) = state.meta.as_ref() else {
        return Err(unavailable_meta());
    };
    match meta.get_object(project_id, bucket, &key) {
        Ok(o) if !o.storage_path.is_empty() => Ok(o),
        Ok(_) => Err(err_json(
            StatusCode::NOT_FOUND,
            "object not found",
            "not_found",
        )),
        Err(MetaError::NotFound) => Err(err_json(
            StatusCode::NOT_FOUND,
            "object not found",
            "not_found",
        )),
        Err(err) => Err(meta_err(err)),
    }
}

fn insert_integrity_headers(headers: &mut HeaderMap, object: &ObjectMeta) {
    headers.insert(
        header::ACCEPT_RANGES,
        HeaderValue::from_static("bytes"),
    );
    if let Some(sha) = object.sha256.as_deref() {
        if let Ok(v) = HeaderValue::from_str(&format!("\"{sha}\"")) {
            headers.insert(header::ETAG, v);
        }
        if let Ok(v) = HeaderValue::from_str(sha) {
            headers.insert(
                header::HeaderName::from_static("x-content-sha256"),
                v,
            );
        }
    }
}

async fn stream_download(
    state: AppState,
    object: ObjectMeta,
    req_headers: HeaderMap,
    rid: String,
    started: Instant,
    include_body: bool,
) -> Response {
    let size = if object.size_bytes >= 0 {
        object.size_bytes as u64
    } else {
        match state.backend.open_object(&object.storage_path).await {
            Ok((_, len)) => len,
            Err(err) => return backend_err(err),
        }
    };

    if state.verify_on_read == VerifyOnRead::Full {
        if let Some(sha) = object.sha256.as_deref() {
            if let Err(err) = state
                .backend
                .verify_object_sha256(&object.storage_path, sha, state.stream_buffer_bytes)
                .await
            {
                state
                    .metrics
                    .storage_integrity_errors_total
                    .fetch_add(1, Ordering::Relaxed);
                return backend_err(err);
            }
        }
    }

    let range_header = req_headers
        .get(header::RANGE)
        .and_then(|v| v.to_str().ok());

    let (status, start, length, content_range) = if let Some(raw) = range_header {
        match parse_bytes_range(raw, size) {
            Ok(r) => {
                state
                    .metrics
                    .storage_range_requests_total
                    .fetch_add(1, Ordering::Relaxed);
                (
                    StatusCode::PARTIAL_CONTENT,
                    r.start,
                    r.len(),
                    Some(r.content_range_header(size)),
                )
            }
            Err(RangeError::Unsatisfiable) => {
                let mut builder = Response::builder().status(StatusCode::RANGE_NOT_SATISFIABLE);
                let headers = builder.headers_mut().unwrap();
                insert_integrity_headers(headers, &object);
                headers.insert(
                    header::CONTENT_RANGE,
                    HeaderValue::from_str(&unsatisfiable_content_range(size))
                        .unwrap_or(HeaderValue::from_static("bytes */0")),
                );
                return builder.body(Body::empty()).unwrap();
            }
            Err(RangeError::Invalid) => {
                // Ignore malformed Range (RFC 7233: servers MAY ignore).
                (StatusCode::OK, 0u64, size, None)
            }
        }
    } else {
        (StatusCode::OK, 0u64, size, None)
    };

    // HEAD: headers only (still honor Range status semantics via Content-Length / Content-Range).
    if !include_body {
        let mut builder = Response::builder().status(status);
        let headers = builder.headers_mut().unwrap();
        let content_type = object
            .content_type
            .as_deref()
            .unwrap_or("application/octet-stream");
        if let Ok(v) = HeaderValue::from_str(content_type) {
            headers.insert(header::CONTENT_TYPE, v);
        }
        headers.insert(
            header::CONTENT_LENGTH,
            HeaderValue::from_str(&length.to_string()).unwrap_or(HeaderValue::from_static("0")),
        );
        insert_integrity_headers(headers, &object);
        if let Some(cr) = content_range.as_deref() {
            if let Ok(v) = HeaderValue::from_str(cr) {
                headers.insert(header::CONTENT_RANGE, v);
            }
        }
        return builder.body(Body::empty()).unwrap();
    }

    let file = if status == StatusCode::PARTIAL_CONTENT {
        match state
            .backend
            .open_object_range(&object.storage_path, start, length)
            .await
        {
            Ok((f, _)) => f,
            Err(err) => return backend_err(err),
        }
    } else {
        match state.backend.open_object(&object.storage_path).await {
            Ok((f, _)) => f,
            Err(err) => return backend_err(err),
        }
    };

    let limited = file.take(length);
    let stream = ReaderStream::with_capacity(limited, state.stream_buffer_bytes);
    let body = Body::from_stream(stream);

    state
        .metrics
        .storage_download_bytes_total
        .fetch_add(length, Ordering::Relaxed);
    state
        .metrics
        .storage_downloads_total
        .fetch_add(1, Ordering::Relaxed);

    let duration_ms = started.elapsed().as_millis() as u64;
    info!(
        project_id = %object.project_id,
        bucket_id = %object.bucket_id,
        key = %object.key,
        size_bytes = length,
        range = content_range.as_deref().unwrap_or("-"),
        duration_ms,
        request_id = %rid,
        "object downloaded"
    );

    let content_type = object
        .content_type
        .as_deref()
        .unwrap_or("application/octet-stream");
    let mut builder = Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, content_type)
        .header(header::CONTENT_LENGTH, length);
    {
        let headers = builder.headers_mut().unwrap();
        insert_integrity_headers(headers, &object);
        if let Some(cr) = content_range.as_deref() {
            if let Ok(v) = HeaderValue::from_str(cr) {
                headers.insert(header::CONTENT_RANGE, v);
            }
        }
    }
    builder.body(body).unwrap()
}
