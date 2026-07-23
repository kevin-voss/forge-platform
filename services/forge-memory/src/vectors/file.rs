//! Fixed-stride memory-mapped vector file (`vectors/<collection>.vec`).

use memmap2::{MmapMut, MmapOptions};
use std::fs::{File, OpenOptions};
use std::io::{self};
use std::path::{Path, PathBuf};

/// Errors from vector file open/read/write.
#[derive(Debug)]
pub enum VectorFileError {
    Io(io::Error),
    Corrupt(String),
    DimensionMismatch { expected: usize, got: usize },
    OutOfBounds { offset: u64, len: u64 },
}

impl std::fmt::Display for VectorFileError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Io(e) => write!(f, "{e}"),
            Self::Corrupt(msg) => write!(f, "corrupt vector file: {msg}"),
            Self::DimensionMismatch { expected, got } => {
                write!(
                    f,
                    "vector dimension mismatch: expected {expected}, got {got}"
                )
            }
            Self::OutOfBounds { offset, len } => {
                write!(f, "vector offset {offset} out of bounds (len={len})")
            }
        }
    }
}

impl std::error::Error for VectorFileError {}

impl From<io::Error> for VectorFileError {
    fn from(value: io::Error) -> Self {
        Self::Io(value)
    }
}

/// Memory-mapped file of fixed-stride `f32` vectors.
pub struct VectorFile {
    path: PathBuf,
    dim: usize,
    file: File,
    /// `None` when the file is empty (zero-length mmap is not portable).
    mmap: Option<MmapMut>,
    /// Number of vector slots currently stored.
    len: u64,
}

impl VectorFile {
    pub fn stride(dim: usize) -> usize {
        dim.saturating_mul(4)
    }

    /// Open or create a vector file for `dim`. Rejects corrupt (non-stride-aligned) sizes.
    pub fn open(path: impl Into<PathBuf>, dim: usize) -> Result<Self, VectorFileError> {
        if dim == 0 {
            return Err(VectorFileError::Corrupt("dim must be > 0".into()));
        }
        let path = path.into();
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(&path)?;
        let meta = file.metadata()?;
        let bytes = meta.len() as usize;
        let stride = Self::stride(dim);
        if !bytes.is_multiple_of(stride) {
            return Err(VectorFileError::Corrupt(format!(
                "{} size {bytes} is not a multiple of stride {stride} (dim={dim})",
                path.display()
            )));
        }
        let len = (bytes / stride) as u64;
        let mmap = if bytes == 0 {
            None
        } else {
            Some(unsafe { MmapOptions::new().map_mut(&file)? })
        };
        Ok(Self {
            path,
            dim,
            file,
            mmap,
            len,
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn dim(&self) -> usize {
        self.dim
    }

    pub fn len(&self) -> u64 {
        self.len
    }

    pub fn is_empty(&self) -> bool {
        self.len == 0
    }

    fn remap(&mut self) -> Result<(), VectorFileError> {
        let bytes = self.file.metadata()?.len();
        if bytes == 0 {
            self.mmap = None;
        } else {
            self.mmap = Some(unsafe { MmapOptions::new().map_mut(&self.file)? });
        }
        Ok(())
    }

    /// Write `vector` at slot `offset`, extending the file when needed.
    pub fn write_at(&mut self, offset: u64, vector: &[f32]) -> Result<(), VectorFileError> {
        if vector.len() != self.dim {
            return Err(VectorFileError::DimensionMismatch {
                expected: self.dim,
                got: vector.len(),
            });
        }
        let stride = Self::stride(self.dim);
        let need_slots = offset + 1;
        if need_slots > self.len {
            let old_bytes = (self.len as usize).saturating_mul(stride);
            let new_bytes = (need_slots as usize).saturating_mul(stride);
            self.file.set_len(new_bytes as u64)?;
            self.remap()?;
            if let Some(mmap) = self.mmap.as_mut() {
                if new_bytes > old_bytes {
                    mmap[old_bytes..new_bytes].fill(0);
                }
            }
            self.len = need_slots;
        } else if self.mmap.is_none() {
            self.remap()?;
        }
        let start = (offset as usize).saturating_mul(stride);
        let end = start + stride;
        let mmap = self
            .mmap
            .as_mut()
            .ok_or_else(|| VectorFileError::Corrupt("mmap missing after grow".into()))?;
        let dst = &mut mmap[start..end];
        for (i, v) in vector.iter().enumerate() {
            let bytes = v.to_le_bytes();
            let o = i * 4;
            dst[o..o + 4].copy_from_slice(&bytes);
        }
        mmap.flush()?;
        Ok(())
    }

    /// Append a vector; returns the assigned slot offset.
    pub fn append(&mut self, vector: &[f32]) -> Result<u64, VectorFileError> {
        let offset = self.len;
        self.write_at(offset, vector)?;
        Ok(offset)
    }

    /// Read the vector at `offset` into a new `Vec<f32>`.
    pub fn read_at(&self, offset: u64) -> Result<Vec<f32>, VectorFileError> {
        if offset >= self.len {
            return Err(VectorFileError::OutOfBounds {
                offset,
                len: self.len,
            });
        }
        let stride = Self::stride(self.dim);
        let start = (offset as usize).saturating_mul(stride);
        let end = start + stride;
        let mmap = self
            .mmap
            .as_ref()
            .ok_or_else(|| VectorFileError::Corrupt("mmap missing for non-empty file".into()))?;
        let src = &mmap[start..end];
        let mut out = Vec::with_capacity(self.dim);
        for i in 0..self.dim {
            let o = i * 4;
            let mut buf = [0u8; 4];
            buf.copy_from_slice(&src[o..o + 4]);
            out.push(f32::from_le_bytes(buf));
        }
        Ok(out)
    }
}

/// Remove a vector file if present (best-effort after collection delete).
pub fn remove_file(path: &Path) -> Result<(), io::Error> {
    match std::fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn write_and_read_at_offset() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("incidents.vec");
        let mut vf = VectorFile::open(&path, 4).unwrap();
        let v0 = vec![1.0, 2.0, 3.0, 4.0];
        let v1 = vec![0.5, -0.5, 0.25, -0.25];
        assert_eq!(vf.append(&v0).unwrap(), 0);
        assert_eq!(vf.append(&v1).unwrap(), 1);
        assert_eq!(vf.read_at(0).unwrap(), v0);
        assert_eq!(vf.read_at(1).unwrap(), v1);
    }

    #[test]
    fn dimension_mismatch_on_write() {
        let dir = tempdir().unwrap();
        let mut vf = VectorFile::open(dir.path().join("t.vec"), 3).unwrap();
        let err = vf.append(&[1.0, 2.0]).unwrap_err();
        assert!(matches!(
            err,
            VectorFileError::DimensionMismatch {
                expected: 3,
                got: 2
            }
        ));
    }

    #[test]
    fn corrupt_size_rejected_on_open() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("bad.vec");
        std::fs::write(&path, [0u8; 10]).unwrap(); // not multiple of 3*4=12
        match VectorFile::open(&path, 3) {
            Err(VectorFileError::Corrupt(_)) => {}
            Err(e) => panic!("expected Corrupt, got {e}"),
            Ok(_) => panic!("expected Corrupt, got Ok"),
        }
    }

    #[test]
    fn survives_reopen() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("persist.vec");
        {
            let mut vf = VectorFile::open(&path, 2).unwrap();
            vf.write_at(0, &[1.5, 2.5]).unwrap();
        }
        let vf = VectorFile::open(&path, 2).unwrap();
        assert_eq!(vf.len(), 1);
        assert_eq!(vf.read_at(0).unwrap(), vec![1.5, 2.5]);
    }
}
