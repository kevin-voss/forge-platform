use crate::log::Logger;
use crate::server::LogEntry;
use reqwest::Client;
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashSet;
use std::sync::{Arc, Mutex};
use std::time::Duration;

#[derive(Clone)]
pub struct EventsConfig {
    pub events_url: String,
    pub consumer_name: String,
    pub identity: String,
    pub subject: String,
    pub poll_ms: u64,
    pub ack_wait_s: u64,
    pub max_deliveries: u32,
}

#[derive(Clone, Default)]
pub struct EventsStatus {
    pub ready: bool,
    pub processed_count: usize,
    pub last_error: Option<String>,
    pub last_incident_id: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ConsumeResponse {
    messages: Option<Vec<ConsumeMessage>>,
}

#[derive(Debug, Deserialize)]
struct ConsumeMessage {
    event_id: String,
    ack_token: String,
    data: Value,
}

pub async fn run_consumer(
    cfg: EventsConfig,
    logger: Logger,
    entries: Arc<Mutex<Vec<LogEntry>>>,
    status: Arc<Mutex<EventsStatus>>,
) {
    let client = Client::builder()
        .timeout(Duration::from_secs(15))
        .build()
        .expect("http client");

    loop {
        match ensure_consumer(&client, &cfg).await {
            Ok(()) => {
                set_status(&status, |s| {
                    s.ready = true;
                    s.last_error = None;
                });
                logger.info(
                    "events consumer ready",
                    &[
                        ("consumer", json!(cfg.consumer_name)),
                        ("subject", json!(cfg.subject)),
                    ],
                );
                break;
            }
            Err(err) => {
                set_status(&status, |s| {
                    s.ready = false;
                    s.last_error = Some(err.clone());
                });
                logger.warn("create consumer retry", &[("error", json!(err))]);
                tokio::time::sleep(Duration::from_secs(1)).await;
            }
        }
    }

    let mut seen: HashSet<String> = HashSet::new();
    let mut interval = tokio::time::interval(Duration::from_millis(cfg.poll_ms.max(100)));
    loop {
        interval.tick().await;
        if let Err(err) = poll_once(&client, &cfg, &logger, &entries, &status, &mut seen).await {
            set_status(&status, |s| s.last_error = Some(err.clone()));
            logger.warn("consume loop error", &[("error", json!(err))]);
        }
    }
}

async fn ensure_consumer(client: &Client, cfg: &EventsConfig) -> Result<(), String> {
    let url = format!("{}/v1/consumers", trim_slash(&cfg.events_url));
    let body = json!({
        "name": cfg.consumer_name,
        "subject": cfg.subject,
        "ack_wait_s": cfg.ack_wait_s,
        "max_deliveries": cfg.max_deliveries,
        "identity": cfg.identity,
    });
    let resp = client
        .post(url)
        .json(&body)
        .send()
        .await
        .map_err(|e| format!("create consumer: {e}"))?;
    let status = resp.status();
    if status.as_u16() == 200 || status.as_u16() == 201 {
        return Ok(());
    }
    let text = resp.text().await.unwrap_or_default();
    Err(format!("create consumer HTTP {}: {}", status.as_u16(), text))
}

async fn poll_once(
    client: &Client,
    cfg: &EventsConfig,
    logger: &Logger,
    entries: &Arc<Mutex<Vec<LogEntry>>>,
    status: &Arc<Mutex<EventsStatus>>,
    seen: &mut HashSet<String>,
) -> Result<(), String> {
    let url = format!("{}/v1/consume", trim_slash(&cfg.events_url));
    let body = json!({
        "consumer": cfg.consumer_name,
        "batch": 10,
    });
    let resp = client
        .post(url)
        .json(&body)
        .send()
        .await
        .map_err(|e| format!("consume: {e}"))?;
    if !resp.status().is_success() {
        let code = resp.status().as_u16();
        let text = resp.text().await.unwrap_or_default();
        return Err(format!("consume HTTP {code}: {text}"));
    }
    let parsed: ConsumeResponse = resp
        .json()
        .await
        .map_err(|e| format!("decode consume: {e}"))?;
    for msg in parsed.messages.unwrap_or_default() {
        handle_message(client, cfg, logger, entries, status, seen, msg).await?;
    }
    Ok(())
}

async fn handle_message(
    client: &Client,
    cfg: &EventsConfig,
    logger: &Logger,
    entries: &Arc<Mutex<Vec<LogEntry>>>,
    status: &Arc<Mutex<EventsStatus>>,
    seen: &mut HashSet<String>,
    msg: ConsumeMessage,
) -> Result<(), String> {
    let incident_id = msg
        .data
        .get("incident_id")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let title = msg
        .data
        .get("title")
        .and_then(|v| v.as_str())
        .unwrap_or("incident")
        .to_string();

    if !seen.contains(&msg.event_id) {
        {
            let mut guard = entries.lock().map_err(|_| "log mutex poisoned".to_string())?;
            guard.push(LogEntry {
                source: "events".into(),
                level: "info".into(),
                message: format!("incident.created {incident_id}: {title}"),
            });
        }
        seen.insert(msg.event_id.clone());
        set_status(status, |s| {
            s.processed_count += 1;
            s.last_incident_id = Some(incident_id.clone());
            s.last_error = None;
        });
        logger.info(
            "incident.created consumed",
            &[
                ("event_id", json!(msg.event_id)),
                ("incident_id", json!(incident_id)),
            ],
        );

        let processed_url = format!("{}/v1/processed", trim_slash(&cfg.events_url));
        let _ = client
            .post(processed_url)
            .json(&json!({
                "consumer": cfg.consumer_name,
                "event_id": msg.event_id,
            }))
            .send()
            .await;
    }

    let ack_url = format!("{}/v1/ack", trim_slash(&cfg.events_url));
    let resp = client
        .post(ack_url)
        .json(&json!({ "ack_token": msg.ack_token }))
        .send()
        .await
        .map_err(|e| format!("ack: {e}"))?;
    if !(resp.status().is_success() || resp.status().as_u16() == 204) {
        let code = resp.status().as_u16();
        let text = resp.text().await.unwrap_or_default();
        return Err(format!("ack HTTP {code}: {text}"));
    }
    Ok(())
}

fn set_status(status: &Arc<Mutex<EventsStatus>>, f: impl FnOnce(&mut EventsStatus)) {
    if let Ok(mut guard) = status.lock() {
        f(&mut guard);
    }
}

fn trim_slash(url: &str) -> &str {
    url.trim_end_matches('/')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn trim_slash_strips_trailing() {
        assert_eq!(trim_slash("http://events:8080/"), "http://events:8080");
        assert_eq!(trim_slash("http://events:8080"), "http://events:8080");
    }

    #[test]
    fn consume_message_parses_incident_id() {
        let raw = r#"{"event_id":"e1","ack_token":"t1","data":{"incident_id":"abc","title":"x"}}"#;
        let msg: ConsumeMessage = serde_json::from_str(raw).expect("parse");
        assert_eq!(msg.event_id, "e1");
        assert_eq!(msg.data["incident_id"], "abc");
    }
}
