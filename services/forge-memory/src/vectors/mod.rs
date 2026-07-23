//! Vector payload storage (mmap files) and tombstone compaction.

mod compact;
mod file;

pub use compact::{compact_all, compact_collection};
pub use file::{remove_file, VectorFile, VectorFileError};
