use serde_json::{Map, Value};
use std::io::{self, Write};
use std::time::{SystemTime, UNIX_EPOCH};

const LEVEL_RANK: &[(&str, u8)] = &[
    ("debug", 10),
    ("info", 20),
    ("warn", 30),
    ("error", 40),
];

#[derive(Clone)]
pub struct Logger {
    service: String,
    min: u8,
}

impl Logger {
    pub fn new(service: impl Into<String>, level: &str) -> Self {
        let min = LEVEL_RANK
            .iter()
            .find(|(name, _)| *name == level)
            .map(|(_, rank)| *rank)
            .unwrap_or(20);
        Self {
            service: service.into(),
            min,
        }
    }

    pub fn info(&self, message: &str, fields: &[(&str, Value)]) {
        self.emit("info", message, fields);
    }

    fn emit(&self, level: &str, message: &str, fields: &[(&str, Value)]) {
        let rank = LEVEL_RANK
            .iter()
            .find(|(name, _)| *name == level)
            .map(|(_, r)| *r)
            .unwrap_or(20);
        if rank < self.min {
            return;
        }

        let mut payload = Map::new();
        payload.insert("timestamp".into(), Value::String(rfc3339_now()));
        payload.insert("level".into(), Value::String(level.into()));
        payload.insert("service".into(), Value::String(self.service.clone()));
        payload.insert("message".into(), Value::String(message.into()));
        for (k, v) in fields {
            payload.insert((*k).into(), v.clone());
        }

        let line = Value::Object(payload).to_string();
        let mut stdout = io::stdout().lock();
        let _ = writeln!(stdout, "{line}");
        let _ = stdout.flush();
    }
}

fn rfc3339_now() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    // Format as YYYY-MM-DDTHH:MM:SSZ without chrono dependency.
    let (y, mo, d, h, mi, s) = civil_from_days(secs);
    format!("{y:04}-{mo:02}-{d:02}T{h:02}:{mi:02}:{s:02}Z")
}

fn civil_from_days(unix_secs: u64) -> (i32, u32, u32, u32, u32, u32) {
    let days = (unix_secs / 86_400) as i64;
    let rem = (unix_secs % 86_400) as u32;
    let h = rem / 3600;
    let mi = (rem % 3600) / 60;
    let s = rem % 60;

    // Algorithm from Howard Hinnant's date algorithms (public domain).
    let z = days + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = (z - era * 146_097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
    let y = (yoe as i64) + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    let y = if m <= 2 { y + 1 } else { y } as i32;
    (y, m, d, h, mi, s)
}
