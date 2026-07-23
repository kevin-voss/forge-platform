//! Access audit trail (metadata only — never secret values).

pub mod hook;
pub mod recorder;
pub mod routes;

pub use recorder::{AuditEvent, AuditRecorder, AuditResult};
