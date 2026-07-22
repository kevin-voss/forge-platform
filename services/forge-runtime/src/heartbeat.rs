use chrono::{DateTime, Utc};
use serde::Serialize;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::task::JoinHandle;
use tracing::{info, warn};

/// In-memory liveness state updated by the periodic heartbeat task.
#[derive(Debug)]
pub struct Heartbeat {
    at: Mutex<DateTime<Utc>>,
    healthy: AtomicBool,
    previously_healthy: AtomicBool,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HeartbeatView {
    pub node_id: String,
    pub at: DateTime<Utc>,
    pub healthy: bool,
}

impl Heartbeat {
    pub fn new() -> Self {
        Self {
            at: Mutex::new(Utc::now()),
            healthy: AtomicBool::new(true),
            previously_healthy: AtomicBool::new(true),
        }
    }

    /// Record a heartbeat tick (used by the background task and unit tests).
    pub fn tick(&self, at: DateTime<Utc>, healthy: bool) {
        {
            let mut guard = self.at.lock().expect("heartbeat mutex");
            *guard = at;
        }
        self.healthy.store(healthy, Ordering::SeqCst);

        let prev = self.previously_healthy.swap(healthy, Ordering::SeqCst);
        if prev != healthy {
            if healthy {
                info!(at = %at, "heartbeat transition: unhealthy → healthy");
            } else {
                warn!(at = %at, "heartbeat transition: healthy → unhealthy");
            }
        }
    }

    pub fn snapshot(&self, node_id: impl Into<String>) -> HeartbeatView {
        let at = *self.at.lock().expect("heartbeat mutex");
        HeartbeatView {
            node_id: node_id.into(),
            at,
            healthy: self.healthy.load(Ordering::SeqCst),
        }
    }

    pub fn last_heartbeat(&self) -> DateTime<Utc> {
        *self.at.lock().expect("heartbeat mutex")
    }

    /// Spawn a supervised heartbeat loop. Panics inside a tick cycle are caught
    /// by restarting the inner loop; the process is never crashed by this task.
    pub fn spawn(self: &Arc<Self>, interval: Duration) -> JoinHandle<()> {
        let state = Arc::clone(self);
        tokio::spawn(async move {
            loop {
                // CatchUnwind-friendly outer supervisor: restart on unexpected exit.
                let inner = Arc::clone(&state);
                let result = tokio::task::spawn(async move {
                    run_loop(inner, interval).await;
                })
                .await;

                match result {
                    Ok(()) => {
                        warn!("heartbeat task ended unexpectedly; restarting");
                    }
                    Err(err) => {
                        warn!(error = %err, "heartbeat task panicked; restarting");
                    }
                }
                tokio::time::sleep(Duration::from_millis(100)).await;
            }
        })
    }
}

impl Default for Heartbeat {
    fn default() -> Self {
        Self::new()
    }
}

async fn run_loop(state: Arc<Heartbeat>, interval: Duration) {
    let mut ticker = tokio::time::interval(interval);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    // First tick completes immediately; record an initial heartbeat.
    ticker.tick().await;
    state.tick(Utc::now(), true);

    loop {
        ticker.tick().await;
        state.tick(Utc::now(), true);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    #[test]
    fn tick_updates_timestamp() {
        let hb = Heartbeat::new();
        let t1 = Utc.with_ymd_and_hms(2026, 7, 22, 12, 0, 0).unwrap();
        let t2 = Utc.with_ymd_and_hms(2026, 7, 22, 12, 0, 10).unwrap();

        hb.tick(t1, true);
        assert_eq!(hb.last_heartbeat(), t1);
        let view = hb.snapshot("node-1");
        assert_eq!(view.node_id, "node-1");
        assert_eq!(view.at, t1);
        assert!(view.healthy);

        hb.tick(t2, true);
        assert_eq!(hb.last_heartbeat(), t2);
    }

    #[tokio::test(start_paused = true)]
    async fn heartbeat_updates_on_interval_with_fake_clock() {
        let hb = Arc::new(Heartbeat::new());
        let before = hb.last_heartbeat();
        let _handle = hb.spawn(Duration::from_secs(10));

        // Allow the spawned task to register the interval and initial tick.
        tokio::task::yield_now().await;
        tokio::time::advance(Duration::from_millis(1)).await;
        tokio::task::yield_now().await;

        let after_start = hb.last_heartbeat();
        assert!(after_start >= before);

        tokio::time::advance(Duration::from_secs(10)).await;
        tokio::task::yield_now().await;
        tokio::time::advance(Duration::from_millis(1)).await;
        tokio::task::yield_now().await;

        let after_interval = hb.last_heartbeat();
        assert!(
            after_interval > after_start,
            "expected heartbeat to advance: {after_start} -> {after_interval}"
        );
    }

    #[test]
    fn serialization_shape() {
        let view = HeartbeatView {
            node_id: "abc".into(),
            at: Utc.with_ymd_and_hms(2026, 7, 22, 12, 0, 0).unwrap(),
            healthy: true,
        };
        let json = serde_json::to_value(&view).unwrap();
        assert_eq!(json["nodeId"], "abc");
        assert_eq!(json["healthy"], true);
        assert!(json.get("at").is_some());
        assert!(json.get("node_id").is_none());
    }
}
