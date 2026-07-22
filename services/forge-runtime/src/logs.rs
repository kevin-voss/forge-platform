//! Workload container log fetch, demux, and formatting.
//!
//! Logs are read on demand from Docker Engine — nothing is persisted here.

use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use serde::Serialize;
use std::pin::Pin;
use std::time::Duration;
use tracing::{info, warn};

use crate::docker::{DockerEngine, RawLogChunk, StreamSource};

/// Query options for `GET /v1/workloads/{id}/logs`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LogsQuery {
    pub tail: u32,
    pub since: Option<DateTime<Utc>>,
    pub streams: StreamSelection,
    pub follow: bool,
    pub format: LogFormat,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StreamSelection {
    Stdout,
    Stderr,
    All,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LogFormat {
    Text,
    Ndjson,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LogLine {
    pub stream: StreamSource,
    pub timestamp: Option<DateTime<Utc>>,
    pub message: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct NdjsonLine<'a> {
    stream: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    timestamp: Option<&'a str>,
    message: &'a str,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LogsError {
    Validation(String),
    NotFound(String),
    Docker(String),
}

impl LogsError {
    pub fn status_code(&self) -> axum::http::StatusCode {
        use axum::http::StatusCode;
        match self {
            Self::Validation(_) => StatusCode::BAD_REQUEST,
            Self::NotFound(_) => StatusCode::NOT_FOUND,
            Self::Docker(_) => StatusCode::BAD_GATEWAY,
        }
    }

    pub fn code(&self) -> &'static str {
        match self {
            Self::Validation(_) => "validation_error",
            Self::NotFound(_) => "not_found",
            Self::Docker(_) => "logs_unavailable",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            Self::Validation(m) | Self::NotFound(m) | Self::Docker(m) => m,
        }
    }
}

/// Parse query parameters for the logs endpoint.
///
/// Supported keys: `tail`, `since` (RFC3339), `streams` (`stdout`|`stderr`|`all`),
/// `follow`, `format` (`text`|`ndjson`).
pub fn parse_logs_query(
    params: &[(String, String)],
    default_tail: u32,
    prefer_ndjson: bool,
) -> Result<LogsQuery, LogsError> {
    let mut tail = default_tail;
    let mut since = None;
    let mut streams = StreamSelection::All;
    let mut follow = false;
    let mut format = if prefer_ndjson {
        LogFormat::Ndjson
    } else {
        LogFormat::Text
    };

    for (key, value) in params {
        match key.as_str() {
            "tail" => {
                let v = value.trim();
                if v.eq_ignore_ascii_case("all") {
                    // Docker accepts "all"; we map to a large finite tail for safety.
                    tail = u32::MAX;
                } else {
                    tail = v.parse::<u32>().map_err(|_| {
                        LogsError::Validation(format!(
                            "tail must be a non-negative integer or 'all', got {value:?}"
                        ))
                    })?;
                }
            }
            "since" => {
                let v = value.trim();
                if v.is_empty() {
                    continue;
                }
                since = Some(parse_since(v)?);
            }
            "streams" => {
                streams = parse_streams(value)?;
            }
            "follow" => {
                follow = parse_boolish(value)?;
            }
            "format" => {
                format = parse_format(value)?;
            }
            // Ignore unknown keys for forward compatibility.
            _ => {}
        }
    }

    Ok(LogsQuery {
        tail,
        since,
        streams,
        follow,
        format,
    })
}

fn parse_since(raw: &str) -> Result<DateTime<Utc>, LogsError> {
    DateTime::parse_from_rfc3339(raw)
        .map(|dt| dt.with_timezone(&Utc))
        .map_err(|_| {
            LogsError::Validation(format!("since must be an RFC3339 timestamp, got {raw:?}"))
        })
}

fn parse_streams(raw: &str) -> Result<StreamSelection, LogsError> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "stdout" => Ok(StreamSelection::Stdout),
        "stderr" => Ok(StreamSelection::Stderr),
        "all" => Ok(StreamSelection::All),
        other => Err(LogsError::Validation(format!(
            "streams must be stdout|stderr|all, got {other:?}"
        ))),
    }
}

fn parse_format(raw: &str) -> Result<LogFormat, LogsError> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "text" | "plain" | "text/plain" => Ok(LogFormat::Text),
        "ndjson" | "json" | "application/x-ndjson" => Ok(LogFormat::Ndjson),
        other => Err(LogsError::Validation(format!(
            "format must be text|ndjson, got {other:?}"
        ))),
    }
}

fn parse_boolish(raw: &str) -> Result<bool, LogsError> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "1" | "true" | "yes" | "on" => Ok(true),
        "0" | "false" | "no" | "off" | "" => Ok(false),
        other => Err(LogsError::Validation(format!(
            "follow must be a boolean, got {other:?}"
        ))),
    }
}

/// Demux Docker's multiplexed log framing (8-byte header + payload).
///
/// Header layout: `[stream:u8][0;3][size:u32_be]`.
/// Stream `1` = stdout, `2` = stderr. Other types are ignored.
///
/// The live path uses bollard's already-demuxed [`LogOutput`][bollard::container::LogOutput];
/// this parser is kept for the demux contract (unit-tested) and raw-frame tooling.
#[allow(dead_code)]
pub fn demux_docker_frames(input: &[u8]) -> Result<Vec<(StreamSource, Vec<u8>)>, String> {
    let mut out = Vec::new();
    let mut offset = 0usize;
    while offset < input.len() {
        if input.len() - offset < 8 {
            return Err(format!(
                "truncated docker log header at offset {offset} ({} bytes left)",
                input.len() - offset
            ));
        }
        let stream_byte = input[offset];
        let size = u32::from_be_bytes([
            input[offset + 4],
            input[offset + 5],
            input[offset + 6],
            input[offset + 7],
        ]) as usize;
        offset += 8;
        if input.len() - offset < size {
            return Err(format!(
                "truncated docker log payload at offset {offset}: need {size}, have {}",
                input.len() - offset
            ));
        }
        let payload = input[offset..offset + size].to_vec();
        offset += size;
        let source = match stream_byte {
            1 => StreamSource::Stdout,
            2 => StreamSource::Stderr,
            _ => continue,
        };
        out.push((source, payload));
    }
    Ok(out)
}

/// Split a Docker log payload (optionally with a leading timestamp) into a [`LogLine`].
pub fn parse_log_payload(stream: StreamSource, payload: &[u8], timestamps: bool) -> LogLine {
    let raw = String::from_utf8_lossy(payload);
    let text = raw.trim_end_matches(['\r', '\n']).to_string();
    if !timestamps {
        return LogLine {
            stream,
            timestamp: None,
            message: text,
        };
    }
    // Docker timestamps: "2024-01-15T12:00:00.123456789Z message"
    if let Some((ts, rest)) = text.split_once(' ') {
        if let Ok(dt) = DateTime::parse_from_rfc3339(ts) {
            return LogLine {
                stream,
                timestamp: Some(dt.with_timezone(&Utc)),
                message: rest.to_string(),
            };
        }
    }
    LogLine {
        stream,
        timestamp: None,
        message: text,
    }
}

pub fn format_log_line(line: &LogLine, format: LogFormat) -> String {
    match format {
        LogFormat::Text => {
            format!("[{}] {}", line.stream.as_str(), line.message)
        }
        LogFormat::Ndjson => {
            let ts = line
                .timestamp
                .map(|t| t.to_rfc3339_opts(chrono::SecondsFormat::Millis, true));
            let row = NdjsonLine {
                stream: line.stream.as_str(),
                timestamp: ts.as_deref(),
                message: &line.message,
            };
            serde_json::to_string(&row).unwrap_or_else(|_| {
                format!(
                    "{{\"stream\":\"{}\",\"message\":{}}}",
                    line.stream.as_str(),
                    serde_json::to_string(&line.message).unwrap_or_else(|_| "\"\"".into())
                )
            })
        }
    }
}

/// Terminal marker for follow streams (SSE `end` event data / ndjson line).
pub fn terminal_marker(reason: &str, format: LogFormat) -> String {
    match format {
        LogFormat::Text => format!("[end] {reason}"),
        LogFormat::Ndjson => format!(
            "{{\"stream\":\"end\",\"reason\":{}}}",
            serde_json::to_string(reason).unwrap_or_else(|_| "\"unknown\"".into())
        ),
    }
}

fn chunk_to_line(chunk: RawLogChunk, timestamps: bool) -> LogLine {
    parse_log_payload(chunk.stream, &chunk.message, timestamps)
}

/// Resolve a managed workload and fetch a bounded log window.
pub async fn fetch_logs(
    docker: &dyn DockerEngine,
    deployment_id: &str,
    query: &LogsQuery,
) -> Result<Vec<LogLine>, LogsError> {
    let view = crate::workload::get_workload(docker, deployment_id)
        .await
        .map_err(map_workload_err)?;

    let opts = docker_log_options(query, false);
    let mut stream = docker.logs(&view.container_id, &opts);
    let timestamps = opts.timestamps;
    let mut lines = Vec::new();
    while let Some(item) = stream.next().await {
        match item {
            Ok(chunk) => lines.push(chunk_to_line(chunk, timestamps)),
            Err(err) => {
                if is_container_gone(&err) {
                    break;
                }
                return Err(LogsError::Docker(err));
            }
        }
    }
    Ok(lines)
}

/// Open a follow stream for a managed workload. Caller must drop the stream on client disconnect.
pub async fn open_follow_stream(
    docker: &dyn DockerEngine,
    deployment_id: &str,
    query: &LogsQuery,
) -> Result<Pin<Box<dyn futures_util::Stream<Item = Result<LogLine, String>> + Send>>, LogsError> {
    let view = crate::workload::get_workload(docker, deployment_id)
        .await
        .map_err(map_workload_err)?;

    let opts = docker_log_options(query, true);
    let timestamps = opts.timestamps;
    let raw = docker.logs(&view.container_id, &opts);
    let mapped = raw.map(move |item| item.map(|chunk| chunk_to_line(chunk, timestamps)));
    Ok(Box::pin(mapped))
}

fn docker_log_options(query: &LogsQuery, follow: bool) -> crate::docker::ContainerLogsOptions {
    let (stdout, stderr) = match query.streams {
        StreamSelection::Stdout => (true, false),
        StreamSelection::Stderr => (false, true),
        StreamSelection::All => (true, true),
    };
    let since = query.since.map(|dt| dt.timestamp()).unwrap_or(0);
    let tail = if query.tail == u32::MAX {
        "all".to_string()
    } else {
        query.tail.to_string()
    };
    // Request Docker timestamps whenever ndjson is asked so operators get wall-clock times.
    let timestamps = matches!(query.format, LogFormat::Ndjson);
    crate::docker::ContainerLogsOptions {
        follow,
        stdout,
        stderr,
        since,
        timestamps,
        tail,
    }
}

fn map_workload_err(err: crate::workload::WorkloadError) -> LogsError {
    match err {
        crate::workload::WorkloadError::NotFound(m) => LogsError::NotFound(m),
        crate::workload::WorkloadError::Validation(m) => LogsError::Validation(m),
        other => LogsError::Docker(other.message().to_string()),
    }
}

pub fn is_container_gone(err: &str) -> bool {
    let lower = err.to_ascii_lowercase();
    lower.contains("no such container")
        || lower.contains("not found")
        || lower.contains("is not running")
        || lower.contains("container dead")
        || lower.contains("container removed")
}

/// Log open/close of a follow stream (duration in seconds).
pub fn log_stream_closed(deployment_id: &str, duration: Duration, reason: &str) {
    info!(
        deployment_id = %deployment_id,
        duration_seconds = duration.as_secs_f64(),
        reason = %reason,
        "log follow stream closed"
    );
}

pub fn log_stream_opened(deployment_id: &str, follow: bool, format: LogFormat) {
    info!(
        deployment_id = %deployment_id,
        follow,
        format = ?format,
        "log stream opened"
    );
}

pub fn warn_stream_error(deployment_id: &str, err: &str) {
    warn!(deployment_id = %deployment_id, error = %err, "log stream error");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn demux_stdout_and_stderr_frames() {
        let mut buf = Vec::new();
        // stdout "hello\n"
        buf.push(1u8);
        buf.extend_from_slice(&[0, 0, 0]);
        buf.extend_from_slice(&6u32.to_be_bytes());
        buf.extend_from_slice(b"hello\n");
        // stderr "boom\n"
        buf.push(2u8);
        buf.extend_from_slice(&[0, 0, 0]);
        buf.extend_from_slice(&5u32.to_be_bytes());
        buf.extend_from_slice(b"boom\n");

        let frames = demux_docker_frames(&buf).expect("demux");
        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].0, StreamSource::Stdout);
        assert_eq!(frames[0].1, b"hello\n");
        assert_eq!(frames[1].0, StreamSource::Stderr);
        assert_eq!(frames[1].1, b"boom\n");
    }

    #[test]
    fn demux_rejects_truncated_payload() {
        let mut buf = Vec::new();
        buf.push(1u8);
        buf.extend_from_slice(&[0, 0, 0]);
        buf.extend_from_slice(&10u32.to_be_bytes());
        buf.extend_from_slice(b"short");
        assert!(demux_docker_frames(&buf).is_err());
    }

    #[test]
    fn query_defaults_and_parsing() {
        let q = parse_logs_query(&[], 100, false).unwrap();
        assert_eq!(q.tail, 100);
        assert!(q.since.is_none());
        assert_eq!(q.streams, StreamSelection::All);
        assert!(!q.follow);
        assert_eq!(q.format, LogFormat::Text);

        let q = parse_logs_query(
            &[
                ("tail".into(), "20".into()),
                ("since".into(), "2024-01-15T12:00:00Z".into()),
                ("streams".into(), "stderr".into()),
                ("follow".into(), "true".into()),
                ("format".into(), "ndjson".into()),
            ],
            100,
            false,
        )
        .unwrap();
        assert_eq!(q.tail, 20);
        assert_eq!(q.since.unwrap().to_rfc3339(), "2024-01-15T12:00:00+00:00");
        assert_eq!(q.streams, StreamSelection::Stderr);
        assert!(q.follow);
        assert_eq!(q.format, LogFormat::Ndjson);
    }

    #[test]
    fn query_rejects_bad_tail_and_since() {
        assert!(parse_logs_query(&[("tail".into(), "nope".into())], 100, false).is_err());
        assert!(parse_logs_query(&[("since".into(), "yesterday".into())], 100, false).is_err());
    }

    #[test]
    fn prefer_ndjson_from_accept() {
        let q = parse_logs_query(&[], 50, true).unwrap();
        assert_eq!(q.tail, 50);
        assert_eq!(q.format, LogFormat::Ndjson);
    }

    #[test]
    fn ndjson_annotation_format() {
        let line = LogLine {
            stream: StreamSource::Stdout,
            timestamp: Some(
                DateTime::parse_from_rfc3339("2024-01-15T12:00:00.123Z")
                    .unwrap()
                    .with_timezone(&Utc),
            ),
            message: "hello".into(),
        };
        let formatted = format_log_line(&line, LogFormat::Ndjson);
        let v: serde_json::Value = serde_json::from_str(&formatted).unwrap();
        assert_eq!(v["stream"], "stdout");
        assert_eq!(v["message"], "hello");
        assert!(v["timestamp"]
            .as_str()
            .unwrap()
            .starts_with("2024-01-15T12:00:00"));
    }

    #[test]
    fn text_format_distinguishes_streams() {
        let out = format_log_line(
            &LogLine {
                stream: StreamSource::Stdout,
                timestamp: None,
                message: "a".into(),
            },
            LogFormat::Text,
        );
        let err = format_log_line(
            &LogLine {
                stream: StreamSource::Stderr,
                timestamp: None,
                message: "b".into(),
            },
            LogFormat::Text,
        );
        assert_eq!(out, "[stdout] a");
        assert_eq!(err, "[stderr] b");
    }

    #[test]
    fn parse_timestamped_payload() {
        let line = parse_log_payload(
            StreamSource::Stdout,
            b"2024-01-15T12:00:00.000000000Z hi there\n",
            true,
        );
        assert_eq!(line.message, "hi there");
        assert!(line.timestamp.is_some());
    }
}
