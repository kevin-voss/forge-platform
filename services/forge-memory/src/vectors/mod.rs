//! Vector payload storage (mmap files).

mod file;

pub use file::{remove_file, VectorFile, VectorFileError};
